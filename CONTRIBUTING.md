# Contributing

This is a small personal packaging project, public so others can use or learn from it. Issues
and pull requests are welcome, but there's no promise they'll be reviewed quickly or merged.

- **Bugs in FigCraft itself** (the server or the web app) belong upstream, in
  [storytold/figcraft](https://github.com/storytold/figcraft). This repository only packages it.
- **Security problems:** see [SECURITY.md](SECURITY.md). Don't open a public issue.
- **Pull requests** target `main`, which is protected: every change needs a review from the
  code owner (`.github/CODEOWNERS`). Keep each one to a single change, and say what you tested.
- **Changing the image** (Dockerfile, `sws.toml`, `web-fonts.txt`, `scripts/`) needs a
  `PACKAGE_REVISION` bump in the Dockerfile: each `<version>-<revision>` tag is published once
  and never overwritten. See the README's "Upgrading".
- **Fonts** added to `web-fonts.txt` must be open-licensed (the web app is public, so the fonts
  can be extracted from it), come from google/fonts at the pinned commit, and carry a sha256 and
  the family's OFL.txt row.

By contributing, you agree your contribution is licensed under MIT OR Apache-2.0, like the rest
of the repository.
