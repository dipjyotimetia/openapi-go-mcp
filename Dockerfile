# GoReleaser builds the binary and copies it into this image — there is no
# `go build` step here. To build a development image locally without
# GoReleaser, run: `goreleaser release --snapshot --clean --skip=publish`.
FROM gcr.io/distroless/static-debian13:nonroot

# dockers_v2 stages binaries under "<os>/<arch>/<binary>" in the build
# context; BuildKit auto-populates TARGETOS and TARGETARCH per platform.
ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/openapi-go-mcp /usr/local/bin/openapi-go-mcp

USER nonroot:nonroot
WORKDIR /workspace

ENTRYPOINT ["/usr/local/bin/openapi-go-mcp"]
