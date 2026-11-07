# figcraft-docker

Container image for [figcraft-server](https://github.com/storytold/figcraft/blob/main/docs/server.md),
built from the upstream release binaries (checked against the release's `SHA256SUMS.txt`) on a
distroless base. Images are published for `linux/amd64` and `linux/arm64`:

```
ghcr.io/kyemcintyre/figcraft-server:<version>   # e.g. 0.16.0
ghcr.io/kyemcintyre/figcraft-server:<major.minor>
ghcr.io/kyemcintyre/figcraft-server:latest
ghcr.io/kyemcintyre/figcraft-server:sha-<commit>
```

## Upgrading FigCraft

Change `ARG FIGCRAFT_VERSION=` in the `Dockerfile` to the new upstream release and push to `main`.
The Release workflow builds and pushes the image tagged with that version. To rebuild the current
version (for base image fixes), run the workflow by hand from the Actions tab.

## Running

The image runs as `nonroot` (uid 65532), keeps its files in `/data`, listens on `47900` and reads
`/etc/figcraft-server/server.toml`. Mount your own `server.toml` to set `public_url`,
`trusted_proxy` and limits:

```toml
listen = "0.0.0.0:47900"
data = "/data"
public_url = "wss://figcraft.example.com/"
```

Run one replica per data directory (a second server refuses to start on it). `GET /healthz`
answers `200 ok` for probes. Management commands go through the same entrypoint:

```sh
kubectl -n figcraft exec deploy/figcraft-server -- \
  /usr/local/bin/figcraft-server --config /etc/figcraft-server/server.toml key
```
