.PHONY: build test test-race vet lint fmt tidy install regen-examples smoke clean release-snapshot

BINARY := openapi-go-mcp
BIN_DIR := bin
# Flags passed to every generator invocation in `regen-examples`. -force
# tells the generator to overwrite the existing *.mcp.go files (which is
# exactly what we want when regenerating); leaving it off would make
# regen-examples fail on second run.
GEN_FLAGS := -force

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

test:
	go test ./...

test-race:
	go test ./... -race -count=1

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

install:
	go install ./cmd/$(BINARY)

# Refresh every example's generated code. Requires oapi-codegen on PATH.
regen-examples: build
	oapi-codegen -config examples/petstore/gen/pet/oapi.yaml examples/petstore/petstore.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/petstore/petstore.yaml \
	    -out examples/petstore/gen/petmcp \
	    -package petmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/petstore/gen/pet

	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec testdata/petstore-v2.json \
	    -emit-v3 examples/swagger2-petstore/petstore-v3.yaml
	oapi-codegen -config examples/swagger2-petstore/gen/pet/oapi.yaml examples/swagger2-petstore/petstore-v3.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/swagger2-petstore/petstore-v3.yaml \
	    -out examples/swagger2-petstore/gen/petmcp \
	    -package petmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/swagger2-petstore/gen/pet

	oapi-codegen -config examples/users-api/gen/users/oapi.yaml examples/users-api/users-api.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/users-api/users-api.yaml \
	    -out examples/users-api/gen/usersmcp \
	    -package usersmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/users-api/gen/users

	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/library/library-v2.json \
	    -emit-v3 examples/library/library-v3.yaml
	oapi-codegen -config examples/library/gen/library/oapi.yaml examples/library/library-v3.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/library/library-v3.yaml \
	    -out examples/library/gen/librarymcp \
	    -package librarymcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/library/gen/library

	oapi-codegen -config examples/complex/gen/complex/oapi.yaml examples/complex/complex.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/complex/complex.yaml \
	    -out examples/complex/gen/complexmcp \
	    -package complexmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/complex/gen/complex

	oapi-codegen -config examples/non-json-bodies/gen/nonjson/oapi.yaml examples/non-json-bodies/non-json-bodies.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/non-json-bodies/non-json-bodies.yaml \
	    -out examples/non-json-bodies/gen/nonjsonmcp \
	    -package nonjsonmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/non-json-bodies/gen/nonjson

	oapi-codegen -config examples/todos/gen/todos/oapi.yaml examples/todos/todos.yaml
	$(BIN_DIR)/$(BINARY) $(GEN_FLAGS) \
	    -spec examples/todos/todos.yaml \
	    -out examples/todos/gen/todosmcp \
	    -package todosmcp \
	    -client-import github.com/dipjyotimetia/openapi-go-mcp/examples/todos/gen/todos

# Quick smoke test: initialise the petstore (go-sdk) MCP server over stdio
# and list tools. Use `make smoke-all` to exercise both backends.
smoke: build
	@( printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; \
	   sleep 1; \
	   printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'; \
	   printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; \
	   sleep 2 ) | go run ./examples/petstore 2>/dev/null | head -2

# Broader smoke: exercise the mark3labs backend too, ensuring the two
# adapter paths stay in lockstep at the protocol layer.
smoke-all: build smoke
	@echo "--- smoke (mark3labs adapter) ---"
	@( printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'; \
	   sleep 1; \
	   printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'; \
	   printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'; \
	   sleep 2 ) | go run ./examples/petstore-mark3labs 2>/dev/null | head -2

clean:
	rm -rf $(BIN_DIR) dist coverage.out

# Validate the goreleaser config without publishing. Useful before tagging.
release-snapshot:
	go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish,docker
