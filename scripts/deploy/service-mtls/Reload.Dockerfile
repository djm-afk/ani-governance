FROM 10.10.1.66:30003/ani-gpu-platform/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0
# Supply a verified, pinned kubectl binary in the build context.
COPY kubectl /usr/local/bin/kubectl
COPY reload.sh /usr/local/bin/reload-service-mtls
USER 65532:65532
ENTRYPOINT ["/bin/sh", "/usr/local/bin/reload-service-mtls"]
