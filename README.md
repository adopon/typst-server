# ponado-typst

Shared Typst -> PDF renderer for ponado apps - "Gotenberg, but only for Typst".

One small Go service (stdlib only) wrapping the prebuilt `typst` CLI. Apps
POST a template over HTTP and get a PDF back, so no project has to bundle
`@myriaddreamin/typst-ts-node-compiler` (tens of MB of wasm/native glue) into
its server build.

```
app (SvelteKit)  --HTTP POST /render-->  ponado-typst  --fork-->  typst compile
```

## Quick start

```bash
docker compose up -d --build          # serves on :3100, joins the `ponado` network
curl -s localhost:3100/health         # ok
```

Build + push a multi-arch image:

```bash
scripts/build-push.sh                 # git.ponado.lt/adomas/ponado-typst:{tag,latest}
TAG=v0.1.1 PUSH=0 scripts/build-push.sh
```

## Layout

```
main.go                  wiring only (config -> renderer -> server)
internal/config/         environment parsing
internal/render/         typst invocation, shadow dirs, timeouts, concurrency
internal/server/         HTTP handlers + JSON/multipart request decoding
```

## API

| Method | Path       | Returns                                            |
| ------ | ---------- | -------------------------------------------------- |
| GET    | `/health`  | `ok` (plain text)                                  |
| GET    | `/version` | `{"service","go","typst"}`                         |
| POST   | `/render`  | `application/pdf`, or `application/json` on error  |

`POST /render` accepts two encodings of the same request.

**JSON** (`Content-Type: application/json`) - files are base64:

```jsonc
{
  "template": "= Hello\n#let data = json(\"/data.json\")", // required
  "mainFilePath": "/main.typ",        // optional, default /main.typ
  "files": { "/data.json": "<base64>" }, // optional shadow files
  "fontPaths": ["/.fonts"],           // optional shadow dirs -> --font-path
  "inputs": { "version": "1.2.0" },   // optional -> sys.inputs.version
  "timeoutMs": 30000                  // optional, 1000-60000 (default 30000)
}
```

**multipart/form-data** - files as binary parts, no base64 overhead:

- text fields: `template`, `mainFilePath`, `timeoutMs`,
  `inputs` (JSON object string), `fontPaths` (JSON array string)
- file parts land flat in the shadow root by filename:
  `curl -F 'files=@data.json'` -> `/data.json`

```bash
# JSON
curl -s localhost:3100/render -H 'content-type: application/json' \
  -d '{"template":"= Ponado typst\nHello from the shared service."}' -o hello.pdf

# multipart (binary files)
curl -s localhost:3100/render \
  -F 'template== Report
#let d = json("data.json")
#d.title' \
  -F 'files=@data.json' -F 'timeoutMs=5000' -o report.pdf
```

- Every request renders in its own temp dir; `read("data.json")` resolves
  relative to the main file's directory.
- Client paths are clamped to the shadow dir - `../` traversal is rejected.
- Error statuses: `400` invalid JSON/template, `413` body too large,
  `408` compile/queue timeout, `422` typst compile error (message included).

## Fonts

Fonts live in the container - clients never upload them:

1. **Baked baseline** - Liberation Sans is installed in the image (invoice and
   certificate templates rely on it; typst's embedded Libertinus alone is not
   enough).
2. **Shared pool** - every `.ttf`/`.otf` under the mounted font dir
   (`TYPST_FONTS_DIR`, default `./fonts`) is visible to all renders:

   ```yaml
   volumes:
     - /srv/fonts:/app/fonts:ro
   ```

   New fonts are picked up on the next render, no rebuild needed.

The raw API still accepts per-request files and `fontPaths` for one-off cases,
but the mounted pool is the intended path: it keeps requests small and fonts
shared across apps.

## Configuration

| Env               | Default | Meaning                                          |
| ----------------- | ------- | ------------------------------------------------ |
| `PORT`            | `3100`  | Listen port.                                      |
| `TYPST_BIN`       | `typst` | Typst binary (path or name on `$PATH`).           |
| `MAX_CONCURRENCY` | NumCPU  | Parallel compiles; `<= 0` = unlimited.            |
| `MAX_BODY_MB`     | `25`    | Request body cap in MB.                           |

## Connecting apps

The service joins a fixed-name docker network `ponado`, so another compose
project reaches it without publishing ports:

```yaml
services:
  app:
    environment:
      TYPST_URL: http://ponado-typst:3100
    networks: [ponado]

networks:
  ponado:
    external: true
```

Without the shared network, point `TYPST_URL` at the published port
(`http://<host>:3100`).

In SvelteKit code the ponado clients read `TYPST_URL` from the server
environment:

- `#ponado/typst/typst.server.js` (registry block) - HTTP-backed
  `TypstRenderer`. Sends multipart by default, takes optional per-render
  `files` (flat names) and `inputs`; fonts come from the service pool.
  `convertToPdf` / `renderWithData` signatures are unchanged.
- `@ponado/document-utils/server` - same contract for the npm package.

When `TYPST_URL` is missing, renders fail at call time with a clear error (the
module can still be imported at build time).

## Version pinning

`TYPST_VERSION` (Dockerfile `ARG`, default `0.15.1`) pins the engine. PDF
output can change between typst releases, so bump it deliberately:
`TYPST_VERSION=0.16.0 scripts/build-push.sh`.

## Compatibility

The `/render` contract is a superset of the algis `pdf-worker` contract
(`template`, `mainFilePath`, `files`, `timeoutMs`), so existing callers can
switch by changing the base URL alone.
