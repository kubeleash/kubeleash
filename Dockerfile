FROM gcr.io/distroless/static:nonroot
# Provided by buildx during the dockers_v2 multi-platform build. GoReleaser lays
# the per-platform binaries out as <os>/<arch>/kubeleash in the build context.
ARG TARGETOS
ARG TARGETARCH
COPY ${TARGETOS}/${TARGETARCH}/kubeleash /usr/bin/kubeleash
ENTRYPOINT ["/usr/bin/kubeleash"]
