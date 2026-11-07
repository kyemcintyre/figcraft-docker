# figcraft-server and the FigCraft web app (https://github.com/storytold/figcraft). The server is
# the upstream release binary, checked against the release's checksums; the web app is compiled
# from the release tag with the extra fonts in web-fonts.txt (see the web stage). Bump
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
#
#   figcraft-admin on 8090: the admin page at /admin (list files and links, make and revoke
#   links, remove files), behind a password it makes and prints in its log on first start
#   (kept hashed in /data/admin; change it on the page, or run figcraft-admin reset-password):
#     --entrypoint /usr/local/bin/figcraft-admin  (env: FIGCRAFT_WEB_URL, see admin/main.go)
#   It needs the server's data volume and server.toml, and runs figcraft-server's commands.
#   FIGCRAFT_SERVER_URL (a ws:// or wss:// address, no query) is written into index.html as the
#   page's own server, for join codes and online sharing; empty leaves the page's default.

ARG FIGCRAFT_VERSION=0.16.0
# The commit the v<FIGCRAFT_VERSION> tag points at, and the craft-fonts commit that release
# pins (craft-fonts-ref in its .github/workflows/release.yml). Update all three together.
ARG FIGCRAFT_COMMIT=f45b261687880e56d1bad11ec82b45131e57ef8b
ARG CRAFT_FONTS_REF=37827adc41fd165f9a9234ac40f94dbad353aa69
ARG PACKAGE_REVISION=5
# The google/fonts commit web-fonts.txt's files and checksums come from.
ARG GOOGLE_FONTS_REF=86aced91de05cd4f96a06838124cca45be337b2b
# Trunk as upstream's release pins it (craft-common-actions install-trunk), x86_64 Linux build.
ARG TRUNK_VERSION=0.21.14
ARG TRUNK_SHA256=f2b4680cd239693a646a2795e4633c625328d7b2a044fbe749fa3a2fe9e7036b
ARG SWS_VERSION=2.44.1
ARG FIGCRAFT_SERVER_URL=""

# The web app, compiled from FigCraft's source at the release tag with the extra fonts in
# web-fonts.txt (the browser app can't download fonts; they're compiled into the .wasm). It's the
# same on every architecture, so it's built once, natively, on an x86_64 build machine. Mirrors
# upstream's release web job (craft-common-actions release.yml): Rust stable, wasm32 target,
# Trunk, the web-release profile (index.html), craft-fonts at the release's craft-fonts-ref.
FROM --platform=$BUILDPLATFORM rust:1-trixie AS web
ARG FIGCRAFT_VERSION
ARG FIGCRAFT_COMMIT
ARG CRAFT_FONTS_REF
ARG GOOGLE_FONTS_REF
ARG TRUNK_VERSION
ARG TRUNK_SHA256
ARG FIGCRAFT_SERVER_URL
SHELL ["/bin/bash", "-euo", "pipefail", "-c"]
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git brotli \
 && rm -rf /var/lib/apt/lists/*
RUN [ "$(uname -m)" = x86_64 ] || { echo "the web app builds on an x86_64 build machine (Trunk is pinned for it)" >&2; exit 1; }; \
    rustup target add wasm32-unknown-unknown; \
    curl --proto '=https' -fsSL -o /tmp/trunk.tar.gz \
      "https://github.com/trunk-rs/trunk/releases/download/v${TRUNK_VERSION}/trunk-x86_64-unknown-linux-gnu.tar.gz"; \
    echo "$TRUNK_SHA256  /tmp/trunk.tar.gz" | sha256sum -c -; \
    tar -xzf /tmp/trunk.tar.gz -C /usr/local/bin trunk; \
    rm /tmp/trunk.tar.gz; \
    trunk --version
WORKDIR /src
# The tag must still be the commit it was when this was pinned (tags can move).
RUN git clone -q --depth 1 --branch "v${FIGCRAFT_VERSION}" https://github.com/storytold/figcraft figcraft; \
    [ "$(git -C figcraft rev-parse HEAD)" = "$FIGCRAFT_COMMIT" ] \
      || { echo "v${FIGCRAFT_VERSION} is $(git -C figcraft rev-parse HEAD), not FIGCRAFT_COMMIT $FIGCRAFT_COMMIT" >&2; exit 1; }; \
    git init -q craft-fonts; \
    git -C craft-fonts fetch -q --depth 1 https://github.com/storytold/craft-fonts "$CRAFT_FONTS_REF"; \
    git -C craft-fonts checkout -q FETCH_HEAD
COPY web-fonts.txt scripts/add-fonts.sh scripts/patch-web-fonts.sh /build/
# 0.16.0's web build ignores FIGCRAFT_WEB_FONTS (only newer source reads it): patch it in.
RUN /build/patch-web-fonts.sh /src/figcraft; \
    /build/add-fonts.sh /build/web-fonts.txt /src/craft-fonts "$GOOGLE_FONTS_REF" > /build/web-fonts.env; \
    echo "embedding: $(cat /build/web-fonts.env)"
# A family the build doesn't embed is only a cargo warning upstream, so fail on that, and check
# each family's name is in the built .wasm (its font catalog entry), as "Noto Sans Georgian" is
# in every release. The build output (several GB in target/) is removed in the same step.
RUN cd figcraft/apps/figcraft-web; \
    CRAFT_FONTS_DIR=/src/craft-fonts CRAFT_FONTS_REQUIRED=1 FIGCRAFT_WEB_FONTS="$(cat /build/web-fonts.env)" \
      trunk build --release 2>&1 | tee /build/trunk.log; \
    if grep -q "isn't embedded" /build/trunk.log; then grep "isn't embedded" /build/trunk.log >&2; exit 1; fi; \
    wasm=$(ls /src/figcraft/dist/web/*_bg.wasm); \
    ls -l "$wasm"; \
    IFS=, read -ra families < /build/web-fonts.env; \
    for family in "${families[@]}"; do \
      if grep -a -q -F "$family" "$wasm"; then echo "embedded: $family"; \
      else echo "not in the built web app: $family" >&2; exit 1; fi; \
    done; \
    rm -rf /src/figcraft/target
RUN set -eu; \
    mkdir -p /out/web; \
    cp -R /src/figcraft/dist/web/. /out/web/; \
    for f in README.md LICENSE-MIT LICENSE-APACHE NOTICE; do cp "/src/figcraft/$f" /out/web/; done; \
    for lic in /src/craft-fonts/fonts/*/OFL.txt; do cp "$lic" "/out/web/OFL-$(basename "$(dirname "$lic")").txt"; done; \
    cp /build/web-fonts.env /out/web/EXTRA-FONTS.txt; \
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

# figcraft-admin (admin/): the password-protected admin page at /admin, which lists published
# files and makes, lists and revokes their links, or removes them, through figcraft-server's own
# commands. Standard library only; cross-compiled for the target architecture.
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS admin
ARG TARGETARCH
WORKDIR /src
COPY admin/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/figcraft-admin .

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
      org.opencontainers.image.description="FigCraft collaboration server and web app (with extra fonts), from upstream releases" \
      org.opencontainers.image.version="${FIGCRAFT_VERSION}-${PACKAGE_REVISION}" \
      org.opencontainers.image.licenses="MIT OR Apache-2.0"
COPY --from=fetch /out/usr /usr
COPY --from=fetch /out/etc/figcraft-server /etc/figcraft-server
COPY --from=fetch --chown=nonroot:nonroot /out/data /data
COPY --from=web /out/web /srv/web
COPY --from=admin /out/figcraft-admin /usr/local/bin/figcraft-admin
COPY sws.toml /etc/static-web-server/sws.toml
# static-web-server reads its settings from here (figcraft-server ignores it).
ENV SERVER_CONFIG_FILE=/etc/static-web-server/sws.toml
USER nonroot:nonroot
VOLUME /data
EXPOSE 47900 8080 8090
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/figcraft-server", "--config", "/etc/figcraft-server/server.toml"]
