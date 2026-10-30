# Bussum

Bussum is a Docker-based API for converting documents to Markdown. It is a fork of [gotenberg/gotenberg](https://github.com/gotenberg/gotenberg), and it keeps that project's module system and multipart HTTP style. The MIT license is unchanged. PDF conversion routes from the upstream project are not part of this fork.

Send files with `multipart/form-data`. Bussum returns Markdown, or a zip when a request produces more than one file.

## Quick start

```bash
docker build -t bussum/bussum:snapshot -f build/Dockerfile .
docker run --rm -p 3000:3000 bussum/bussum:snapshot
```

Convert a text file:

```bash
curl \
  --request POST http://localhost:3000/forms/markdown/convert \
  --form files=@notes.txt \
  -o notes.md
```

## Routes

- `POST /forms/markdown/convert` for `.txt`, `.md`, `.html`, `.htm`, Office documents, and PDF files
- `POST /forms/markdown/convert/url` for an HTTP or HTTPS page, rendered with Chromium

`flavour` is `gfm` (the default) or `commonmark`. `extractImages` defaults to false. `Gotenberg-Output-Filename`, `Gotenberg-Trace`, and the webhook headers are the same names as upstream.

The engine contract is in [docs/markdown-architecture.md](docs/markdown-architecture.md).

## What stays from upstream

Chromium renders URLs. LibreOffice opens Office documents. Poppler reads PDF input. The API, logging, OpenTelemetry, and Prometheus modules are the upstream ones. CLI flags and HTTP header names keep the Gotenberg prefix so those contracts stay stable.

## Contributing

- Contributing guide: [CONTRIBUTING.md](CONTRIBUTING.md)
- Upstream project: [gotenberg/gotenberg](https://github.com/gotenberg/gotenberg)
- License: [MIT](LICENSE)
