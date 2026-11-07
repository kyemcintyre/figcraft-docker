# figcraft-docker

Container image for [FigCraft](https://github.com/storytold/figcraft)'s collaboration server
([figcraft-server](https://github.com/storytold/figcraft/blob/main/docs/server.md)) and its browser
app, built from the upstream release files (each checked against its release's checksums) on a
distroless base. Images are published for `linux/amd64` and `linux/arm64`:

```
ghcr.io/kyemcintyre/figcraft-server:<version>-<revision>   # e.g. 0.16.0-2: pin this; never re-pushed
ghcr.io/kyemcintyre/figcraft-server:<version>              # floating: newest packaging of that version
ghcr.io/kyemcintyre/figcraft-server:<major.minor>
ghcr.io/kyemcintyre/figcraft-server:latest
ghcr.io/kyemcintyre/figcraft-server:sha-<commit>
```

## Upgrading

- **New FigCraft release:** set `ARG FIGCRAFT_VERSION=` in the `Dockerfile` to it and
  `ARG PACKAGE_REVISION=1`, then push to `main`.
- **Packaging change only** (this repo's files): bump `ARG PACKAGE_REVISION=` and push.

The Release workflow builds and pushes the image. It refuses to overwrite an existing
`<version>-<revision>` tag, so a push that forgets the bump fails rather than changing what's
deployed. Running it by hand from the Actions tab rebuilds only the floating tags.

## What's in it

One image, two programs. Run each in its own container (in Kubernetes, two containers in one pod):

| Program | Port | Start | Reads |
|---|---|---|---|
| `figcraft-server` (default entrypoint) | 47900 | as is | `/etc/figcraft-server/server.toml`, data in `/data` |
| `static-web-server` (the web app in `/srv/web`) | 8080 | entrypoint `/usr/local/bin/static-web-server` | `/etc/static-web-server/sws.toml` |

Both run as `nonroot` (uid 65532). The web app's `.wasm`/`.js`/`.html` ship with Brotli and gzip
copies; hashed assets are cached for a year and `index.html` is revalidated. `GET /health` on 8080
and `GET /healthz` on 47900 answer for probes.

### The server

Mount your own `server.toml` to set `public_url`, `trusted_proxy` and limits:

```toml
listen = "0.0.0.0:47900"
data = "/data/server"   # a subdirectory: the server chmods its data directory to 0700
public_url = "wss://figcraft.example.com/"
```

Run one replica per data directory (a second server refuses to start on it). Management commands
go through the same entrypoint:

```sh
kubectl -n figcraft exec deploy/figcraft -c server -- \
  /usr/local/bin/figcraft-server --config /etc/figcraft-server/server.toml key
```

### The web app

The browser app can't download fonts: they're compiled into it. So the image compiles it from
FigCraft's source at the release tag (the same way upstream's release does) with the extra
families in [`web-fonts.txt`](web-fonts.txt) (Bodoni Moda, Jost, Parisienne), taken from
[google/fonts](https://github.com/google/fonts) at a pinned commit and checked against their
sha256. To add a family, add its rows (open-licensed fonts only: the app is public) and bump
`PACKAGE_REVISION`. A FigCraft upgrade also updates `FIGCRAFT_COMMIT` and `CRAFT_FONTS_REF` in
the `Dockerfile` (the tag's commit and its `release.yml`'s `craft-fonts-ref`). The compile takes
20-40 minutes on GitHub's runners; the server binary is still the upstream release's.

FigCraft 0.16.0's web build only embeds a fixed list of fonts and ignores `FIGCRAFT_WEB_FONTS`
(newer source supports it), so [`scripts/patch-web-fonts.sh`](scripts/patch-web-fonts.sh) changes
that one condition in `crates/text/build.rs` when the source doesn't read the variable itself. It
fails if the line has changed, and the build checks each family's name is in the built `.wasm`,
so a web app without the fonts is never published.

`FIGCRAFT_SERVER_URL` in the Release workflow (`wss://figcraft.kyemcintyre.net/`) is written into
`index.html` as the page's own server, so join codes and online sharing use it. Clients open a file
in the browser, without installing FigCraft, at:

```
https://figcraft.kyemcintyre.net/#join=<share link or join code>
```

Paste the view (or edit) link from the app's Share popover after `#join=`. Everything after `#`
stays in the browser and never reaches the web server's logs. Clients need a current browser with
WebGL2 or WebGPU (Safari 16.4 or later); the first load downloads about 5 MiB.

Serve the web app and the server under one hostname by sending WebSocket upgrades (and `/healthz`)
to 47900 and everything else to 8080.

## Licence

The files in this repository are licensed under either of [Apache License 2.0](LICENSE-APACHE) or
[MIT](LICENSE-MIT), at your option, like FigCraft itself. The image bundles FigCraft,
static-web-server and open-licensed fonts under their own licences: see [NOTICE](NOTICE).
Security reports: [SECURITY.md](SECURITY.md). Contributions: [CONTRIBUTING.md](CONTRIBUTING.md).
