# Security policy

## Reporting a vulnerability

Please report security problems **privately**, not in a public issue: use
[Report a vulnerability](https://github.com/kyemcintyre/figcraft-docker/security/advisories/new)
(GitHub private vulnerability reporting). Include what's affected, how to reproduce it and what
it could lead to. You'll get a reply as soon as practical; this is a one-person project.

## Scope

This repository packages software it doesn't write. Report here problems in **this packaging**:
the Dockerfile, the scripts, `sws.toml`, the release workflow, or how the image is built,
verified or published (`ghcr.io/kyemcintyre/figcraft-server`).

Problems in the bundled software itself go to its own project:

- figcraft-server and the FigCraft web app:
  [storytold/figcraft](https://github.com/storytold/figcraft)
- static-web-server:
  [static-web-server/static-web-server](https://github.com/static-web-server/static-web-server)

## Supported versions

Only the newest image (`latest`, and the newest `<version>-<revision>` tag) gets fixes. Older
tags stay published and unchanged, but aren't patched: move to the newest one.
