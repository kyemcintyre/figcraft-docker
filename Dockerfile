# figcraft-server and the FigCraft web app (https://github.com/storytold/figcraft) packaged from
# the upstream release files, each checked against its release's checksums. Bump
# FIGCRAFT_VERSION to upgrade, and PACKAGE_REVISION for a change to this packaging alone; the
# release workflow tags the image <FIGCRAFT_VERSION>-<PACKAGE_REVISION> (never re-pushed), plus
# the floating <FIGCRAFT_VERSION>, <major.minor> and latest.
#
#   docker build -t figcraft-server .
#
# One image, two programs; run each in its own container (in Kubernetes, two containers in one
# pod) rather than both in one:
#
#   figcraft-server (the default entrypoint) on 47900: the collaboration server. Runs as nonroot
#   (uid 65532), reads /etc/figcraft-server/server.toml (listen on 0.0.0.0:47900, data in
#   /data); mount your own to set public_url and the rest. SIGTERM saves open files first.
#
#   static-web-server on 8080: the browser app from /srv/web, precompressed (Brotli, gzip),
#   hashed assets cached for a year and index.html revalidated, /health for probes:
#     --entrypoint /usr/local/bin/static-web-server  (reads /etc/static-web-server/sws.toml)
#   Open https://<host>/#join=<share link or join code> to join a file in the browser.
#   FIGCRAFT_SERVER_URL (a ws:// or wss:// address, no query) is written into index.html as the
#   page's own server, for join codes and online sharing; empty leaves the page's default.

ARG FIGCRAFT_VERSION=0.16.0
ARG PACKAGE_REVISION=2
ARG SWS_VERSION=2.44.1
ARG FIGCRAFT_SERVER_URL=""

# The web app is the same on every architecture: fetch, patch and compress it once, natively.
FROM --platform=$BUILDPLATFORM debian:trixie-slim AS web
ARG FIGCRAFT_VERSION
ARG FIGCRAFT_SERVER_URL
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl unzip brotli \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /dl
RUN set -eu; \
    NAME="figcraft-web-${FIGCRAFT_VERSION}"; \
    BASE="https://github.com/storytold/figcraft/releases/download/v${FIGCRAFT_VERSION}"; \
    curl -fsSLO "$BASE/$NAME.zip"; \
    curl -fsSLO "$BASE/SHA256SUMS.txt"; \
    grep " $NAME.zip\$" SHA256SUMS.txt | sha256sum -c -; \
    unzip -q "$NAME.zip"; \
    mkdir -p /out; \
    mv "$NAME" /out/web; \
    rm -f /out/web/.htaccess /out/web/_headers; \
    if [ -n "$FIGCRAFT_SERVER_URL" ]; then \
      case "$FIGCRAFT_SERVER_URL" in \
        ws://*|wss://*) ;; \
        *) echo "FIGCRAFT_SERVER_URL must start with ws:// or wss://" >&2; exit 1 ;; \
      esac; \
      case "$FIGCRAFT_SERVER_URL" in \
        *[\"\<\>\'\?\&\|\ ]*) echo "FIGCRAFT_SERVER_URL has characters a meta tag can't hold" >&2; exit 1 ;; \
      esac; \
      sed -i "s|<title>FigCraft</title>|<title>FigCraft</title>\n  <meta name=\"figcraft-server\" content=\"$FIGCRAFT_SERVER_URL\">|" /out/web/index.html; \
      grep -q "name=\"figcraft-server\" content=\"$FIGCRAFT_SERVER_URL\"" /out/web/index.html; \
    fi; \
    find /out/web -type f \( -name '*.wasm' -o -name '*.js' -o -name '*.html' \) \
      -exec brotli -q 11 -k {} \; -exec gzip -9 -k {} \;

# The two binaries, for the target architecture.
FROM debian:trixie-slim AS fetch
ARG FIGCRAFT_VERSION
ARG SWS_VERSION
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
    printf 'listen = "0.0.0.0:47900"\ndata = "/data"\n' > /out/etc/figcraft-server/server.toml; \
    SWS="static-web-server-v${SWS_VERSION}-${ARCH}-unknown-linux-musl"; \
    SBASE="https://github.com/static-web-server/static-web-server/releases/download/v${SWS_VERSION}"; \
    curl -fsSLO "$SBASE/$SWS.tar.gz"; \
    curl -fsSLO "$SBASE/static-web-server-v${SWS_VERSION}-SHA256SUM"; \
    grep "  $SWS.tar.gz\$" "static-web-server-v${SWS_VERSION}-SHA256SUM" | head -n1 | sha256sum -c -; \
    tar -xzf "$SWS.tar.gz"; \
    install -D -m 0755 "$SWS/static-web-server" /out/usr/local/bin/static-web-server; \
    mkdir -p /out/usr/share/doc/static-web-server; \
    cp "$SWS/LICENSE-MIT" "$SWS/LICENSE-APACHE" /out/usr/share/doc/static-web-server/

# glibc and libgcc only, no shell; "nonroot" is uid 65532.
FROM gcr.io/distroless/cc-debian13:nonroot
ARG FIGCRAFT_VERSION
ARG PACKAGE_REVISION
LABEL org.opencontainers.image.title="figcraft-server" \
      org.opencontainers.image.description="FigCraft collaboration server and web app, packaged from upstream release files" \
      org.opencontainers.image.version="${FIGCRAFT_VERSION}-${PACKAGE_REVISION}" \
      org.opencontainers.image.licenses="MIT OR Apache-2.0"
COPY --from=fetch /out/usr /usr
COPY --from=fetch /out/etc/figcraft-server /etc/figcraft-server
COPY --from=fetch --chown=nonroot:nonroot /out/data /data
COPY --from=web /out/web /srv/web
COPY sws.toml /etc/static-web-server/sws.toml
# static-web-server reads its settings from here (figcraft-server ignores it).
ENV SERVER_CONFIG_FILE=/etc/static-web-server/sws.toml
USER nonroot:nonroot
VOLUME /data
EXPOSE 47900 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/figcraft-server", "--config", "/etc/figcraft-server/server.toml"]
