# typst-server

Self-hosted Typst -> PDF renderer - "Gotenberg, but only for Typst".

One small Go service (stdlib only) wrapping the prebuilt `typst` CLI. Clients
POST a template over HTTP and get a PDF back, so no project has to bundle
`@myriaddreamin/typst-ts-node-compiler` (tens of MB of wasm/native glue) into
its server build.

```
client  --HTTP POST /render-->  typst-server  --fork-->  typst compile
```

## Quick start

```bash
docker compose up -d                                # pull the prebuilt image, serve on :3100
docker compose -f compose.build.yml up -d --build   # or build the local tree instead
curl -s localhost:3100/health                       # ok
```

`compose.yml` is the copy-pasteable, production one: it only pulls the
published image (`compose.yml` picks up `TYPST_IMAGE` if you publish to a
different registry/tag). Published to both the internal registry and GHCR:

```bash
docker compose up -d
TYPST_IMAGE=ghcr.io/adopon/typst-server:latest docker compose up -d
```

`compose.build.yml` is the local-build variant used during development.

Build + push a multi-arch image (internal registry **and** GHCR from one build):

```bash
pnpm install                                        # once; installs @ponado/docker-image
pnpm exec ponado-docker-image --mode prod           # v0.1.2, v0.1, latest, sha-<short>
pnpm exec ponado-docker-image --mode prod --dry-run # print tags + commands
```

There is no per-repo build script: all tag/mirror/platform policy lives in
`package.json` → `dockerImage`, and the shared CLI is the only entry point. The
first target is built once and the GHCR mirror is copied from it with
`docker buildx imagetools create`, so both registries get the same digest.

Overrides go straight to the CLI:

```bash
pnpm exec ponado-docker-image --mode prod --image registry.example.com/you/typst-server --no-mirror
pnpm exec ponado-docker-image --mode prod --tag v0.1.2-rc.1
pnpm exec ponado-docker-image build --mode dev --no-push   # local --load
pnpm exec ponado-docker-image                             # interactive
```

The prod mode refuses a dirty tree, a non-`main` branch, or a HEAD that is
not tagged `v<version>`. For a scratch build add
`--allow-dirty --allow-untagged --allow-branch`, or use `--mode dev`.

GHCR auth needs a **classic** personal access token with `write:packages`
(fine-grained tokens are not supported by GitHub Packages). Create one with only
that scope, avoiding the `repo` scope the UI adds by default:

<https://github.com/settings/tokens/new?scopes=write:packages>

Then log in (keep the token in your password manager, not in the repo):

```bash
export CR_PAT=ghp_...
echo "$CR_PAT" | docker login ghcr.io -u adopon --password-stdin
```

The `gh` CLI token also works if you refresh it with the scope
(`gh auth refresh -h github.com -s write:packages`), but a dedicated PAT keeps
Docker's stored credential separate from your `gh` session.

The resulting package is private by default; make it public in the package
settings if `docker pull` should work without a login.

A PAT push creates a **user-scoped** package (`github.com/adopon?tab=packages`),
so it does not appear under the repository's Packages tab until it is linked.
The first time, link it manually: package settings → *Connect repository* →
`adopon/typst-server`. New packages pick this up automatically from the
`org.opencontainers.image.source` label in the Dockerfile.

## Build caching

Rebuilds are cheap even though the service ships two runtimes (Go + typst):

- the Go stage runs on the **build platform** and cross-compiles for the
  target (`GOARCH=$TARGETARCH`, CGO off), so the arm64 leg of a multi-arch
  build never executes the toolchain under QEMU;
- Go build and module caches are **BuildKit cache mounts**, so a source change
  recompiles only what changed (measured: ~21s first amd64 build, ~3s rebuild;
  ~15s first arm64 build, ~2s rebuild);
- Docker **layer cache** covers base images, fonts and the typst copy.

For CI or several build machines, export the cache to the registry (needs a
`docker-container` builder - the default docker driver cannot export caches):

```bash
docker buildx create --name typst --driver docker-container --use
pnpm exec ponado-docker-image --mode prod \
  --cache-to type=registry,ref=registry.example.com/you/typst-server:buildcache,mode=max \
  --cache-from type=registry,ref=registry.example.com/you/typst-server:buildcache
```

## Layout

```
main.go                  wiring only (config -> renderer -> server)
internal/config/         environment parsing
internal/render/         typst invocation, shadow dirs, timeouts, concurrency
internal/server/         HTTP handlers + JSON/multipart request decoding
package.json             version, dockerImage policy, release scripts
```

## API

| Method | Path       | Returns                                           |
| ------ | ---------- | ------------------------------------------------- |
| GET    | `/health`  | `ok` (plain text)                                 |
| GET    | `/version` | `{"service","version","go","typst"}`              |
| POST   | `/render`  | `application/pdf`, or `application/json` on error |

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
  -d '{"template":"= typst-server\nHello from the shared service."}' -o hello.pdf

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

1. **Baked baseline** - Liberation Sans is installed in the image (templates
   that need a common sans fallback rely on it; typst's embedded Libertinus
   alone is not enough).
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

The service joins a fixed-name docker network (`typst`), so another compose
project reaches it without publishing ports:

```yaml
services:
  app:
    environment:
      TYPST_URL: http://typst-server:3100
    networks: [typst]

networks:
  typst:
    external: true
```

Override the network name with `TYPST_NETWORK`. Without the shared network,
point clients at the published port (`http://<host>:3100`) instead.

Any HTTP client works: POST to `/render` with either encoding above.

## Versioning & releases

Two independent versions:

- **Service version** - `package.json` `version` is the single source of truth.
  The shared build CLI tags the image `v<version>` and bakes the version into
  the binary and the OCI label, so `/version` reports the exact build. Image
  tags follow the shared policy: `vX.Y.Z`, `vX.Y`, `latest`, `sha-<short>`.
- **Engine version** - `TYPST_VERSION` (Dockerfile `ARG`, default `0.15.1`)
  pins the typst CLI. PDF output can change between typst releases, so bump it
  deliberately:
  `pnpm exec ponado-docker-image --mode prod --build-arg TYPST_VERSION=0.16.0`.

Cut a release with `bumpp` (bumps `package.json`, commits, tags `vX.Y.Z`,
pushes), then the same command builds and pushes the image:

```bash
pnpm release:patch     # or release:minor / release:major
pnpm release           # choose the bump interactively
```

Both refuse a non-`main` branch and `bumpp --git-check` refuses a dirty tree
before anything is tagged. To do the git step only:

```bash
pnpm exec bumpp patch --git-check --yes
# later, after reviewing the pushed tag
pnpm exec ponado-docker-image --mode prod
```

## Compatibility

The `/render` contract is a superset of the minimal pdf-worker contract
(`template`, `mainFilePath`, `files`, `timeoutMs`), so existing callers can
switch by changing the base URL alone.

## License

MIT - see [LICENSE](LICENSE).
