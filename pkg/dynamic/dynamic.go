// Copyright 2026 Dipjyoti Metia.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package dynamic registers MCP tools directly from an OpenAPI document at
// process startup. It shares the generator's operation collection and proxy
// request semantics, without writing generated source to disk.
package dynamic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/dipjyotimetia/openapi-go-mcp/pkg/generator"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/loader"
	"github.com/dipjyotimetia/openapi-go-mcp/pkg/runtime"
)

// Config controls dynamic registration.
//
// The source is deployment-owned, trusted configuration, not an end-user
// input. SourceHTTPClient is used only to download an HTTPS OpenAPI document.
// UpstreamHTTPClient is used only for requests made by registered tools. The
// two clients are deliberately separate: a transport trusted to reach a
// private API must never be used to fetch a remotely supplied document.
//
// BaseURL overrides the first OpenAPI server URL; when it is empty, that
// server URL and its declared variable defaults are used. A remote document
// always requires BaseURL so an untrusted document cannot select an arbitrary
// upstream target. ServerVariables overrides those defaults by name.
type Config struct {
	SourceHTTPClient   *http.Client
	UpstreamHTTPClient *http.Client
	BaseURL            string
	NamePrefix         string
	RequestTimeout     time.Duration
	// AllowInsecureAuth permits authenticated calls to a local HTTP upstream.
	// The default requires HTTPS whenever an operation applies credentials.
	AllowInsecureAuth bool
	// MaxResponseBytes limits upstream response buffering. Zero uses the
	// runtime default, which is bounded for production safety.
	MaxResponseBytes int64
	// MTLSConfigured must be true when UpstreamHTTPClient is configured with a
	// client certificate for an OpenAPI mutualTLS scheme. The explicit signal
	// prevents a regular custom client from satisfying that requirement.
	MTLSConfigured  bool
	ServerVariables map[string]string
	// Provider selects the input-schema dialect advertised to the MCP client.
	// The zero value selects the standard MCP-compatible schema.
	Provider runtime.LLMProvider
	// RequestAuthProvider supplies deployment-specific OIDC/SigV4-style
	// request signing for operations that declare custom security schemes.
	RequestAuthProvider runtime.RequestAuthProvider
}

// Register loads source, collects the proxy-mode operations, and registers
// every included operation with server. source must be a local path or HTTPS
// URL. A remote source must come from trusted deployment configuration;
// transport checks cannot make an arbitrary user-provided URL SSRF-safe.
// Registration is deliberately startup-only: the spec is read and every input
// validator is prepared once, before the server starts serving tools.
func Register(ctx context.Context, server runtime.MCPServer, source string, cfg Config) error {
	if server == nil {
		return fmt.Errorf("dynamic registration requires an MCP server")
	}
	isRemote, err := validateSource(source)
	if err != nil {
		return err
	}
	doc, err := loadSource(ctx, source, cfg.SourceHTTPClient)
	if err != nil {
		return fmt.Errorf("load OpenAPI source: %w", err)
	}
	ops, _, err := generator.CollectOperations(doc, generator.Options{Mode: generator.ModeProxy})
	if err != nil {
		return fmt.Errorf("collect OpenAPI operations: %w", err)
	}
	if isRemote && hasClientCredentialsFlow(ops) {
		return fmt.Errorf("remote OpenAPI sources may not declare OAuth client-credentials token URLs; use a trusted local spec, a pre-acquired access token, or a custom request auth provider")
	}
	baseURL, serverVariables, err := resolveBaseURL(doc, cfg, isRemote)
	if err != nil {
		return err
	}
	mtlsConfigured := cfg.MTLSConfigured
	client := cfg.UpstreamHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	client = runtime.HTTPClientWithoutRedirects(client)
	toolConfig := &runtime.Config{NamePrefix: cfg.NamePrefix}
	oauthStates := newOAuthCredentialStates(ops)
	for _, op := range ops {
		registerOperation(server, op, baseURL, serverVariables, client, cfg.RequestTimeout, cfg.MaxResponseBytes, toolConfig, cfg.Provider, cfg.RequestAuthProvider, mtlsConfigured, cfg.AllowInsecureAuth, oauthStates)
	}
	return nil
}

func validateSource(source string) (bool, error) {
	if source == "" {
		return false, fmt.Errorf("OpenAPI source is empty")
	}
	u, err := url.Parse(source)
	if err != nil {
		return false, fmt.Errorf("parse OpenAPI source %q: %w", source, err)
	}
	if u.Scheme == "" {
		return false, nil // A filesystem path; loader gives useful path errors.
	}
	if !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.Hostname() == "" {
		return false, fmt.Errorf("OpenAPI source must be a local path or absolute HTTPS URL, got %q", source)
	}
	if u.User != nil {
		return false, fmt.Errorf("OpenAPI source URL must not contain credentials")
	}
	if u.Fragment != "" {
		return false, fmt.Errorf("OpenAPI source URL must not contain a fragment")
	}
	return true, nil
}

func loadSource(ctx context.Context, source string, client *http.Client) (*openapi3.T, error) {
	remote, err := validateSource(source)
	if err != nil {
		return nil, err
	}
	if remote {
		return loadRemoteSource(ctx, source, sourceClientWithoutRedirects(client))
	}
	return loader.Load(ctx, source)
}

func loadRemoteSource(ctx context.Context, source string, client *http.Client) (*openapi3.T, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, loader.DefaultURLLoadTimeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("new source request: %w", err)
	}
	req.Header.Set("Accept", "application/json, application/yaml;q=0.9, */*;q=0.5")
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", source, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch %s: HTTP %d", source, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, loader.DefaultMaxSpecSize+1))
	if err != nil {
		return nil, fmt.Errorf("read source body: %w", err)
	}
	if int64(len(raw)) > loader.DefaultMaxSpecSize {
		return nil, fmt.Errorf("source body exceeds %d bytes", loader.DefaultMaxSpecSize)
	}
	if err := rejectExternalReferences(raw); err != nil {
		return nil, err
	}

	file, err := os.CreateTemp("", "openapi-go-mcp-dynamic-*.yaml")
	if err != nil {
		return nil, fmt.Errorf("create temporary source file: %w", err)
	}
	path := file.Name()
	defer func() { _ = os.Remove(path) }()
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write temporary source file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close temporary source file: %w", err)
	}
	return loader.Load(ctx, path)
}

// rejectExternalReferences keeps remote specs as a single, explicitly
// authorized HTTPS fetch. kin-openapi follows external refs while parsing; if
// they were allowed here, a remote document could cause secondary requests to
// a private network even though its own URL was validated.
func rejectExternalReferences(raw []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return nil // loader.Load returns the authoritative syntax diagnostic.
	}
	return walkYAMLNode(&document)
}

func walkYAMLNode(node *yaml.Node) error {
	node = dereferenceAlias(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := dereferenceAlias(node.Content[i]), dereferenceAlias(node.Content[i+1])
			if key != nil && value != nil && key.Value == "$ref" && (value.Kind != yaml.ScalarNode || !strings.HasPrefix(value.Value, "#")) {
				return fmt.Errorf("remote OpenAPI source contains external $ref %q; save the fully bundled spec locally", value.Value)
			}
			if err := walkYAMLNode(value); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range node.Content {
		if err := walkYAMLNode(child); err != nil {
			return err
		}
	}
	return nil
}

func dereferenceAlias(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	return node
}

func sourceClientWithoutRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone
}

func resolveBaseURL(doc *openapi3.T, cfg Config, remoteSource bool) (string, map[string]string, error) {
	vars := make(map[string]string, len(cfg.ServerVariables))
	if cfg.BaseURL != "" {
		for name, value := range cfg.ServerVariables {
			vars[name] = value
		}
		return validateBaseURL(cfg.BaseURL, vars, remoteSource)
	}
	if remoteSource {
		return "", nil, fmt.Errorf("remote OpenAPI source requires dynamic.Config.BaseURL; refusing document-controlled upstream URL")
	}
	if doc == nil || len(doc.Servers) == 0 || doc.Servers[0] == nil || doc.Servers[0].URL == "" {
		return "", nil, fmt.Errorf("OpenAPI document has no server URL; set dynamic.Config.BaseURL")
	}
	for name, variable := range doc.Servers[0].Variables {
		if variable != nil {
			vars[name] = variable.Default
		}
	}
	for name, value := range cfg.ServerVariables {
		vars[name] = value
	}
	resolved, err := runtime.SubstituteServerVariables(doc.Servers[0].URL, vars)
	if err != nil {
		return "", nil, fmt.Errorf("resolve OpenAPI server URL: %w", err)
	}
	if strings.Contains(resolved, "{") {
		return "", nil, fmt.Errorf("OpenAPI server URL %q has an unresolved variable", doc.Servers[0].URL)
	}
	return validateBaseURL(resolved, vars, false)
}

func validateBaseURL(baseURL string, vars map[string]string, requireHTTPS bool) (string, map[string]string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", nil, fmt.Errorf("parse dynamic upstream base URL %q: %w", baseURL, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Hostname() == "" {
		return "", nil, fmt.Errorf("dynamic upstream base URL must be an absolute HTTP(S) URL, got %q", baseURL)
	}
	if requireHTTPS && u.Scheme != "https" {
		return "", nil, fmt.Errorf("remote OpenAPI source requires an HTTPS dynamic upstream base URL")
	}
	if u.User != nil {
		return "", nil, fmt.Errorf("dynamic upstream base URL must not contain credentials")
	}
	if u.Fragment != "" {
		return "", nil, fmt.Errorf("dynamic upstream base URL must not contain a fragment")
	}
	return baseURL, vars, nil
}

func registerOperation(server runtime.MCPServer, op generator.Operation, baseURL string, serverVariables map[string]string, client *http.Client, timeout time.Duration, maxResponseBytes int64, toolConfig *runtime.Config, provider runtime.LLMProvider, authProvider runtime.RequestAuthProvider, mtlsConfigured, allowInsecureAuth bool, oauthStates map[string]*oauthCredentialState) {
	tool := runtime.ApplyConfig(runtime.Tool{
		Name:            op.ToolName,
		Description:     op.Description,
		RawInputSchema:  inputSchemaForProvider(provider, op.InputSchemaJSON, op.InputSchemaOpenAIJSON),
		RawOutputSchema: json.RawMessage(op.OutputSchemaJSON),
		Annotations:     op.Annotations,
	}, toolConfig)
	validator := runtime.CompileInputValidator(tool.RawInputSchema)
	server.AddTool(tool, func(ctx context.Context, call *runtime.CallToolRequest) (*runtime.CallToolResult, error) {
		if call == nil {
			return runtime.HandleError(&runtime.ToolError{Status: http.StatusBadRequest, Code: "invalid_arguments", Message: "tool call is nil"})
		}
		if err := validator.Validate(call.Arguments); err != nil {
			return runtime.HandleError(err)
		}
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		httpReq, err := buildRequest(ctx, op, baseURL, serverVariables, call.Arguments)
		if err != nil {
			return runtime.HandleError(err)
		}
		if err := applySecurity(ctx, httpReq, op, authProvider, mtlsConfigured, allowInsecureAuth, oauthStates); err != nil {
			return runtime.HandleError(err)
		}
		response, err := client.Do(httpReq)
		if err != nil {
			return runtime.HandleError(err)
		}
		body, err := runtime.ReadResponseBodyLimit(response, maxResponseBytes)
		if err != nil {
			return runtime.HandleError(err)
		}
		return runtime.NewToolResultFromHTTP(response.StatusCode, response.Header, body, op.ResponseContentType), nil
	})
}

func inputSchemaForProvider(provider runtime.LLMProvider, standard, openAI string) json.RawMessage {
	if provider == runtime.LLMProviderOpenAI && openAI != "" {
		return json.RawMessage(openAI)
	}
	return json.RawMessage(standard)
}

func buildRequest(ctx context.Context, op generator.Operation, baseURL string, serverVariables map[string]string, args map[string]any) (*http.Request, error) {
	path := op.Path
	for _, param := range op.PathParams {
		serialized, _, err := runtime.SerializeProxyParam(args, runtime.ProxyParamSpec{
			Name: param.Name, In: "path", Style: param.Style, Explode: param.Explode, AllowReserved: param.AllowReserved,
		}, true)
		if err != nil {
			return nil, err
		}
		path = strings.ReplaceAll(path, "{"+param.Name+"}", serialized.Value)
	}
	query := runtime.ProxyQuery{}
	for _, param := range op.QueryParams {
		serialized, present, err := runtime.SerializeProxyParam(args, runtime.ProxyParamSpec{
			Name: param.Name, In: "query", Style: param.Style, Explode: param.Explode, AllowReserved: param.AllowReserved,
		}, param.Required)
		if err != nil {
			return nil, err
		}
		if present {
			query = append(query, serialized.Query...)
		}
	}
	resolvedBaseURL, err := runtime.SubstituteServerVariables(baseURL, serverVariables)
	if err != nil {
		return nil, err
	}
	target, err := runtime.BuildProxyURL(resolvedBaseURL, path, query)
	if err != nil {
		return nil, err
	}
	body, contentType, err := requestBody(op, args)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, op.Method, target, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for _, param := range op.HeaderParams {
		serialized, present, err := runtime.SerializeProxyParam(args, runtime.ProxyParamSpec{
			Name: param.Name, In: "header", Style: param.Style, Explode: param.Explode, AllowReserved: param.AllowReserved,
		}, param.Required)
		if err != nil {
			return nil, err
		}
		if present {
			req.Header.Set(param.Name, serialized.Value)
		}
	}
	for _, param := range op.CookieParams {
		serialized, present, err := runtime.SerializeProxyParam(args, runtime.ProxyParamSpec{
			Name: param.Name, In: "cookie", Style: param.Style, Explode: param.Explode, AllowReserved: param.AllowReserved,
		}, param.Required)
		if err != nil {
			return nil, err
		}
		if present {
			for _, cookie := range serialized.Cookies {
				req.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value}) // #nosec G124
			}
		}
	}
	return req, nil
}

func requestBody(op generator.Operation, args map[string]any) (io.Reader, string, error) {
	if !op.HasRequestBody {
		return nil, "", nil
	}
	switch op.RequestBodyKind {
	case generator.BodyJSON:
		raw, ok := args["body"]
		if !ok || raw == nil {
			return nil, "", nil
		}
		return runtime.EncodeJSONBody(raw)
	case generator.BodyForm:
		return runtime.EncodeFormBody(args)
	case generator.BodyMultipart:
		contentType, body, err := runtime.BuildMultipartBody(args, op.RequestFileFields)
		return body, contentType, err
	case generator.BodyOctet:
		body, err := runtime.BuildBase64BytesBody(args)
		return body, "application/octet-stream", err
	case generator.BodyText, generator.BodyRaw:
		body, err := runtime.BuildStringBody(args)
		return body, op.RequestContentType, err
	default:
		return nil, "", fmt.Errorf("unsupported request body kind %q", op.RequestBodyKind)
	}
}

func applySecurity(ctx context.Context, req *http.Request, op generator.Operation, provider runtime.RequestAuthProvider, mtlsConfigured, allowInsecureAuth bool, oauthStates map[string]*oauthCredentialState) error {
	if !op.AuthRequired {
		return nil
	}
	for _, alternative := range op.Security {
		if !securityAvailable(alternative, provider, mtlsConfigured) {
			continue
		}
		if !allowInsecureAuth && (req.URL == nil || req.URL.Scheme != "https") {
			return fmt.Errorf("authenticated operation %s %s requires an HTTPS upstream URL", op.Method, op.Path)
		}
		for _, scheme := range alternative {
			if err := applyScheme(ctx, req, scheme, provider, mtlsConfigured, oauthStates); err != nil {
				return err
			}
		}
		return nil
	}
	return &runtime.UnsatisfiedSecurityError{Operation: op.Method + " " + op.Path, MissingEnvVars: requiredEnvVars(op.Security)}
}

func securityAvailable(schemes []generator.SecurityScheme, provider runtime.RequestAuthProvider, mtlsConfigured bool) bool {
	for _, scheme := range schemes {
		if scheme.Kind == generator.SecurityCustom {
			if !runtime.RequestAuthProviderAvailable(provider, scheme.Name) {
				return false
			}
			continue
		}
		if scheme.Kind == generator.SecurityMTLS {
			if !mtlsConfigured {
				return false
			}
			continue
		}
		if scheme.Kind == generator.SecurityHTTPBasic {
			if os.Getenv(scheme.UsernameEnvVar) == "" || os.Getenv(scheme.PasswordEnvVar) == "" {
				return false
			}
			continue
		}
		if scheme.Kind == generator.SecurityOAuth2 && scheme.OAuthTokenURL != "" {
			if os.Getenv(scheme.EnvVar) == "" && (os.Getenv(scheme.ClientIDEnvVar) == "" || os.Getenv(scheme.ClientSecretEnvVar) == "") {
				return false
			}
			continue
		}
		if os.Getenv(scheme.EnvVar) == "" {
			return false
		}
	}
	return true
}

func applyScheme(ctx context.Context, req *http.Request, scheme generator.SecurityScheme, provider runtime.RequestAuthProvider, mtlsConfigured bool, oauthStates map[string]*oauthCredentialState) error {
	switch scheme.Kind {
	case generator.SecurityAPIKey:
		return runtime.ApplyAPIKey(req, runtime.AuthLocation(scheme.In), scheme.ParamName, os.Getenv(scheme.EnvVar))
	case generator.SecurityHTTPBearer:
		return runtime.ApplyBearer(req, os.Getenv(scheme.EnvVar))
	case generator.SecurityOAuth2:
		if token := os.Getenv(scheme.EnvVar); token != "" {
			return runtime.ApplyBearer(req, token)
		}
		state := oauthStates[oauthStateKey(scheme)]
		if state == nil {
			return &runtime.MissingCredentialError{SchemeName: scheme.Name, EnvVar: scheme.EnvVar}
		}
		return state.apply(ctx, req, scheme)
	case generator.SecurityHTTPBasic:
		return runtime.ApplyBasic(req, os.Getenv(scheme.UsernameEnvVar), os.Getenv(scheme.PasswordEnvVar))
	case generator.SecurityCustom:
		return runtime.ApplyRequestAuthForSecurityScheme(ctx, req, provider, scheme.Name)
	case generator.SecurityMTLS:
		if !mtlsConfigured {
			return fmt.Errorf("mTLS client is not configured for security scheme %s", scheme.Name)
		}
		if req.URL == nil || req.URL.Scheme != "https" {
			return fmt.Errorf("mTLS security scheme %s requires an HTTPS upstream URL", scheme.Name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported security scheme %q", scheme.Name)
	}
}

type oauthCredentialState struct {
	sync.Mutex
	provider     *runtime.ClientCredentialsProvider
	clientID     string
	clientSecret string
	scopes       string
}

func newOAuthCredentialStates(ops []generator.Operation) map[string]*oauthCredentialState {
	states := map[string]*oauthCredentialState{}
	for _, op := range ops {
		for _, alternative := range op.Security {
			for _, scheme := range alternative {
				if scheme.Kind == generator.SecurityOAuth2 && scheme.OAuthTokenURL != "" {
					states[oauthStateKey(scheme)] = &oauthCredentialState{}
				}
			}
		}
	}
	return states
}

func oauthStateKey(scheme generator.SecurityScheme) string {
	return scheme.Name + "\x00" + strings.Join(scheme.OAuthRequestScopes, " ")
}

func (state *oauthCredentialState) apply(ctx context.Context, req *http.Request, scheme generator.SecurityScheme) error {
	clientID := os.Getenv(scheme.ClientIDEnvVar)
	clientSecret := os.Getenv(scheme.ClientSecretEnvVar)
	if clientID == "" || clientSecret == "" {
		return &runtime.MissingCredentialError{SchemeName: scheme.Name, EnvVar: scheme.ClientIDEnvVar + "/" + scheme.ClientSecretEnvVar}
	}
	state.Lock()
	scopeKey := strings.Join(scheme.OAuthRequestScopes, " ")
	if state.provider == nil || state.clientID != clientID || state.clientSecret != clientSecret || state.scopes != scopeKey {
		provider, err := runtime.NewClientCredentialsProvider(runtime.ClientCredentialsConfig{
			TokenURL: scheme.OAuthTokenURL, ClientID: clientID, ClientSecret: clientSecret, Scopes: scheme.OAuthRequestScopes,
		})
		if err != nil {
			state.Unlock()
			return err
		}
		state.provider = provider
		state.clientID = clientID
		state.clientSecret = clientSecret
		state.scopes = scopeKey
	}
	provider := state.provider
	state.Unlock()
	return provider.Apply(ctx, req)
}

func hasClientCredentialsFlow(ops []generator.Operation) bool {
	for _, op := range ops {
		for _, alternative := range op.Security {
			for _, scheme := range alternative {
				if scheme.Kind == generator.SecurityOAuth2 && scheme.OAuthTokenURL != "" {
					return true
				}
			}
		}
	}
	return false
}

func requiredEnvVars(alternatives [][]generator.SecurityScheme) []string {
	seen := map[string]struct{}{}
	var vars []string
	for _, alternative := range alternatives {
		for _, scheme := range alternative {
			for _, variable := range schemeEnvVars(scheme) {
				if _, ok := seen[variable]; !ok {
					seen[variable] = struct{}{}
					vars = append(vars, variable)
				}
			}
		}
	}
	return vars
}

func schemeEnvVars(scheme generator.SecurityScheme) []string {
	if scheme.Kind == generator.SecurityCustom {
		return nil
	}
	if scheme.Kind == generator.SecurityHTTPBasic {
		return []string{scheme.UsernameEnvVar, scheme.PasswordEnvVar}
	}
	if scheme.Kind == generator.SecurityOAuth2 && scheme.OAuthTokenURL != "" {
		return []string{scheme.EnvVar, scheme.ClientIDEnvVar, scheme.ClientSecretEnvVar}
	}
	return []string{scheme.EnvVar}
}
