FROM gcr.io/distroless/static:nonroot
# Provided by buildx during the dockers_v2 multi-platform build. GoReleaser lays
# the per-platform binaries out as <os>/<arch>/kubeleash in the build context.
ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/kubeleash /usr/bin/kubeleash
# Proves to the MCP Registry that this image belongs to the io.github.kubeleash
# namespace — the registry rejects an OCI package whose annotation/label does
# not match the `name` in server.json. Must stay in lockstep with server.json.
LABEL io.modelcontextprotocol.server.name="io.github.kubeleash/kubeleash"
ENTRYPOINT ["/usr/bin/kubeleash"]
