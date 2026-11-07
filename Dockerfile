# figcraft-server (https://github.com/storytold/figcraft) packaged from the upstream release
# binaries, checked against the release's SHA256SUMS.txt. Bump FIGCRAFT_VERSION to upgrade; the
# release workflow tags the image with this version.
#
#   docker build -t figcraft-server .
#
# Runs as nonroot (uid 65532), keeps files in /data and reads /etc/figcraft-server/server.toml
# (listen on 0.0.0.0:47900, data in /data). Mount your own server.toml to set public_url,
# trusted_proxy and limits. `docker stop` / pod termination sends SIGTERM and the server saves
# open files before exiting.

ARG FIGCRAFT_VERSION=0.16.0

FROM debian:trixie-slim AS fetch
ARG FIGCRAFT_VERSION
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /dl
RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) ARCH=x86_64 ;; \
      arm64) ARCH=aarch64 ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    NAME="figcraft-server-${FIGCRAFT_VERSION}-linux-${ARCH}"; \
    BASE="https://github.com/storytold/figcraft/releases/download/v${FIGCRAFT_VERSION}"; \
    curl -fsSLO "$BASE/$NAME.tar.gz"; \
    curl -fsSLO "$BASE/SHA256SUMS.txt"; \
    grep " $NAME.tar.gz\$" SHA256SUMS.txt | sha256sum -c -; \
    tar -xzf "$NAME.tar.gz"; \
    install -D -m 0755 "$NAME/figcraft-server" /out/usr/local/bin/figcraft-server; \
    mkdir -p /out/etc/figcraft-server /out/data /out/usr/share/doc/figcraft-server; \
    cp "$NAME/LICENSE-MIT" "$NAME/LICENSE-APACHE" "$NAME/NOTICE" /out/usr/share/doc/figcraft-server/; \
    printf 'listen = "0.0.0.0:47900"\ndata = "/data"\n' > /out/etc/figcraft-server/server.toml

# glibc and libgcc only, no shell; "nonroot" is uid 65532.
FROM gcr.io/distroless/cc-debian13:nonroot
ARG FIGCRAFT_VERSION
LABEL org.opencontainers.image.title="figcraft-server" \
      org.opencontainers.image.description="FigCraft collaboration server, packaged from upstream release binaries" \
      org.opencontainers.image.version="${FIGCRAFT_VERSION}" \
      org.opencontainers.image.licenses="MIT OR Apache-2.0"
COPY --from=fetch /out/usr /usr
COPY --from=fetch /out/etc/figcraft-server /etc/figcraft-server
COPY --from=fetch --chown=nonroot:nonroot /out/data /data
USER nonroot:nonroot
VOLUME /data
EXPOSE 47900
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/figcraft-server", "--config", "/etc/figcraft-server/server.toml"]
