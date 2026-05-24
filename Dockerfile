FROM gcr.io/distroless/static:nonroot
COPY kubeleash /usr/bin/kubeleash
ENTRYPOINT ["/usr/bin/kubeleash"]
