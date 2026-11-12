# TritonTube validation record

For the latest interview rehearsal, running service URLs, exact results and Git
status, see [INTERVIEW_VERIFICATION.md](INTERVIEW_VERIFICATION.md).

**Current result:** Phases 1–6 and the optional three-member etcd backend pass
local tests. See [PHASE7_RESULTS.md](PHASE7_RESULTS.md) for the core-before-optional
gates, final regular/race results and actual etcd leader-failure continuity output.
[PHASE6_RESULTS.md](PHASE6_RESULTS.md) records the preceding container phase.
Earlier Docker blockers and optional-backend deferrals below are historical.

Executed on **November 11, 2026 (America/Los_Angeles)**, on macOS arm64.
This records actual local outcomes; it does not assert that CI or Docker ran.

## Environment and tooling

- Initially empty repository (only `.git`), remote
  `https://github.com/Shreyasi08/TritonTube.git`. Existing files were preserved.
- Git available; no configured author identity. No commits or pushes made.
- Go absent from PATH. Downloaded official Go 1.27.4 darwin-arm64 into
  `/tmp/tritontube-tools/go`, checked the archive SHA-256 against the official
  download metadata: `91a44a2b8a716600ad94a4f143e156116620db8828086a6e3602291be9bd0bb4`.
- A local wrapper `/tmp/tritontube-go-env` sets PATH, module/build caches and GOBIN
  under `/tmp`; it does not modify the host's installed toolchains or shell profile.
- FFmpeg 7.1.1 and ffprobe installed; protoc 3.20.3 installed.
- Docker CLI/Compose installed, but the Docker daemon is not running.
- Initial sandbox tests could not bind loopback ports; reran with authorized local
  networking. The following phase gates passed under that execution context.

## Phase gates

All phases below ran **gofmt, `go test ./...`, and `go build ./...`** successfully
before the next phase began. No test failures remain from those gates.

| Phase | Principal additions | Actual result |
| --- | --- | --- |
| 1 | Module, protobuf/generated bindings, `internal/storage`, `cmd/storage`, storage tests, initial README/plan | Streaming lifecycle, corruption rejection, traversal/symlink confinement, cancellation passed |
| 2 | `internal/hashring`, `internal/cluster` router and tests | Determinism, SHA-256 truncation, boundary/wraparound/collision/remapping tests; three-node routing passed |
| 3 | `internal/metadata`, `internal/api`, `cmd/web`, structured logging, integration fixtures | SQLite persistence/rollback, HTTP upload/source retrieval, missing/invalid uploads passed |
| 4 | `internal/transcode`, embedded `web` assets, watch/stream handlers | Real MP4s with/without audio transcoded; DASH manifest/media retrieval and FFmpeg HTTP decoding passed; failure cleanup passed |
| 5 | Migration coordinator/state persistence, health API, `cmd/admin`, migration tests | Add/remove, restart routing, partial-copy failure, rejected metadata commit, retained sources passed |
| 6 | Dockerfile/Compose, Makefile, sample script, CI, README/DESIGN, expanded tests | Regular tests/build/vet/race tests passed; configuration validated; compiled-binary smoke passed |

## Final checks

Commands below were executed with the temporary Go environment wrapper:

```sh
/tmp/tritontube-go-env gofmt -l cmd internal proto tests web
/tmp/tritontube-go-env go test ./...
/tmp/tritontube-go-env go build ./...
/tmp/tritontube-go-env go vet ./...
/tmp/tritontube-go-env go test -race ./...
/tmp/tritontube-go-env make build
/tmp/tritontube-go-env env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./...
docker compose config --quiet
```

Results: formatting clean; all tests passed; builds/vet passed; race detector found
no races; Linux amd64 pure-Go cross-build passed; Compose configuration accepted.
The final test expansion also passed regular/race tests and build. Both video
representations and optional audio are explicitly selected for HTTP DASH decoding.
No generated MP4, SQLite database or temporary media lives in the repository.
Generated executables exist in ignored `bin/` for immediate local use.

Additional tests cover simultaneous HTTP uploads, twelve concurrent gRPC uploads,
oversized multipart rejection, byte ranges, missing media, CLI nonzero health
exit status and rejection of concurrent membership changes. The checked-in CI
workflow runs these commands with FFmpeg installed, but no remote CI run is claimed.

## Compiled executable smoke test

Using isolated temporary data directories and ephemeral loopback ports:

1. Started **three compiled storage binaries** and the **compiled web binary**.
2. Generated a six-second H.264/AAC MP4 using `scripts/generate-sample.sh`.
3. Uploaded via HTTP multipart; received HTTP 201 with `status: ready`.
4. FFmpeg read the HTTP MPD, fetched segments and decoded both video representations
   and audio successfully.
5. Started a fourth compiled storage process and used the compiled admin CLI to add
   it. Migration committed with **1 affected/copied file**; HTTP DASH decoding passed.
6. Removed node2 with the CLI. Migration committed with **6 affected/copied files**.
   Stopped that storage process. HTTP DASH decoding still passed.
7. CLI list and health succeeded with the new three-node topology.
8. Stopped/restarted web against the same SQLite database and original seed flags.
   Persisted membership was used and HTTP DASH decoding passed again.
9. Stopped all smoke-test processes. All media/logs/databases stayed in the host
   temporary directory, outside the repository.

The separate automated migration tests use 80-file inventories and exercise failed
partial migrations/metadata commits with no premature source deletion.

## Unverified and deferred

- **Docker execution/build (initially blocked, now resolved):** the first validation
  had no running daemon. Phase 6 started Docker Desktop and passed image build,
  real container health/startup, non-root volume writes and media/migration/restart
  testing. See PHASE6_RESULTS.md for the passing output.
- **Visual browser playback/UI:** browser access host unavailable. Native Chrome
  fallback timed out. HTTP pages/static assets returned 200, the pinned dash.js
  5.2.1 CDN URL returned 200, and DASH media decoded through HTTP; no claim of a
  visually observed browser playback session is made.
- **Optional phase 7 etcd:** deferred; no backend, deployment or failover test exists.
  The SQLite path and migration guarantees are implemented and tested independently.

## Focused Phase 1–2 review

A subsequent storage/hash-ring review added regression coverage and reproduced two
streaming-client defects: short destination writes were reported as successful
Downloads, and an early Upload rejection could be hidden behind a Send-side EOF.
Both were fixed in `internal/storage/client.go`. Downloads now return
`io.ErrShortWrite`; interrupted sends receive the server's final gRPC status.

Additional tests reject duplicate upload headers and oversized chunks and verify
that neither publishes a file or leaves staging data. SHA-256 determinism,
collision handling, wraparound, limited remapping and three-node routing passed.

Executed successfully after the fixes (with the temporary Go wrapper):

```sh
go test -count=1 -v ./internal/storage ./internal/hashring ./internal/cluster
go test -count=1 ./...
go build ./...
go test -race -count=1 ./internal/storage ./internal/hashring ./internal/cluster
```

Formatting was applied with gofmt. This review changed only Phase 1 storage-client
code, its tests and this validation record; existing later-phase implementations
were preserved. No development beyond Phase 2 was undertaken in this review.

## Focused Phase 3–4 implementation and genuine media verification

Phase 3 review retained the existing SQLite repository and MP4 endpoints and fixed
final metadata-update failures leaving uploads in an in-progress state. Failure
responses now include a video ID and metadata Location header; failed status uses
an independent context so request cancellation does not suppress the attempt.
Added tests for finalization failure and restart recovery of interrupted states.
The complete test suite and build passed before proceeding to Phase 4.

Phase 4 now checks that FFmpeg produced a regular, nonempty MPD and output files
before publishing, bounds filter-complex threads, rejects non-finite durations,
preserves context cancellation in subprocess errors and surfaces cleanup failures.
Added real FFmpeg publication-failure and missing-encoder tests to prove temporary
output cleanup, failed metadata, source inventory retention and non-playable failed
videos. Existing embedded library UI and pinned dash.js player are preserved.

The genuine end-to-end test was executed with FFmpeg installed (no media-test skip):

```sh
/tmp/tritontube-go-env ./scripts/test-e2e.sh
```

Actual output:

```text
MP4 92289 bytes → SQLite ready record → 8 inventoried files across 3 nodes → HTTP DASH decoded (360p, 720p, audio=false)
MP4 121413 bytes → SQLite ready record → 11 inventoried files across 3 nodes → HTTP DASH decoded (360p, 720p, audio=true)
PASS
```

This test generates genuine H.264 MP4s, uploads multipart bodies over HTTP, runs
ffprobe/FFmpeg, writes real SQLite metadata, stores files via three gRPC servers,
parses the actual MPD to verify 360p and 720p representations, checks library and
player routes/title escaping, and decodes both representations plus optional audio
via the HTTP manifest and media endpoints. `make test-e2e` exposes the same check;
the script fails explicitly when Go, FFmpeg or ffprobe is missing.

After Phase 4 changes, all of these passed:

```sh
go test -count=1 ./...
go build ./...
go vet ./...
go test -race -count=1 ./...
make build
```

All commands used the temporary Go environment wrapper; gofmt reported no remaining
unformatted files. Binaries were rebuilt in ignored `bin/`. Temporary test media and
SQLite files remained outside the repository. Browser access was checked again:
the browser host is still unavailable, so visual dash.js playback is unverified;
actual HTTP DASH decoding passed. No later-phase development, commits or pushes
were performed during this review.

## Focused Phase 5 CLI and migration verification

Reviewed and strengthened `cmd/admin`, `internal/cluster`, the storage identity
protocol and migration tests. The CLI supports list/add/remove/health/migration.
Add/remove now reject even HTTP 2xx responses that do not report a committed
migration. New routers have an explicit idle state, and migration preflight uses
bounded per-node health and identity checks.

A new regression reproduced a safety gap: `localhost:PORT` could be added under a
new node name even though it reached the same storage process as `127.0.0.1:PORT`.
That could cause post-commit source cleanup to delete the destination too.
Fixed this by adding a persistent per-directory `.node-id` and protobuf Info RPC.
Existing and proposed membership must have distinct storage identities before any
copying. Tests prove identity persistence across restarts, hidden identity exclusion
from file inventories, and rejection of the alias with all original data retained.
Generated protobuf bindings and executables were rebuilt. Older storage binaries
must be restarted with the new build before migration; absent Info support causes
explicit failure and preserves existing routing.

Executed the generated-MP4/compiled-CLI test and checksum/alias fault tests:

```sh
go test -count=1 -v ./tests \
  -run 'TestAdminNodeChangesKeepDASHPlayable|TestMigrationRejects' -timeout 2m
```

The generated H.264/AAC video was uploaded through HTTP to SQLite-backed metadata
and three real loopback gRPC servers. A separately compiled admin process performed
list, add, remove, health and migration commands against the HTTP API. The joining
identity was chosen to ensure at least one media file actually changed owners.

Actual focused-run results after the identity fix:

- Initial HTTP DASH playback decoded both video representations and audio.
- CLI add committed **1 copied/affected file**; HTTP playback decoded successfully.
- CLI graceful removal of node3 committed **5 copied/affected files**. The test
  stopped the removed gRPC server, checked every HTTP file against its inventoried
  size/SHA-256, and decoded both video representations and audio again.
- Reconstructing the coordinator from SQLite's committed membership, while the
  removed node remained stopped, retained successful HTTP DASH playback.
- Corrupted destination read-back was rejected despite a successful Upload RPC;
  old membership, ownership and good sources were retained.
- Corrupted source data was rejected against its recorded checksum; no membership
  commit or source deletion occurred. Other good files remained accessible.
- Address-alias addition was rejected before copying; all good sources were retained.

Existing tests also passed for old/new-ring ownership, 80-file redistribution,
partial-transfer failure, metadata commit failure and concurrent-change rejection.
The final Phase 5 gate passed:

```sh
go test -count=1 ./...
go build ./...
go vet ./...
go test -race -count=1 ./...
make build
```

Commands used `/tmp/tritontube-go-env` for the isolated toolchain. Formatting was
clean. The media test is now included in `make test-e2e`, alongside the Phase 4
upload/transcode/decode cases. These checks validate actual HTTP DASH decoding;
visual browser playback was not observed. All test media, databases and compiled
CLI test artifacts used temporary directories. No commits or pushes were made.

## Phase 6 final result

Docker Desktop started successfully. Compose image builds and real container tests
passed, including health/startup, non-root volume writes, generated MP4 upload,
DASH decoding, node join, persisted four-node membership after web restart,
graceful departure and playback after stopping that node. Final regular/race
suites, vet, formatting, executable/package builds, protobuf regeneration and module
verification all passed. The isolated project's containers and volumes were removed.
Exact commands, captured outputs and fixed harness failures are documented in
[PHASE6_RESULTS.md](PHASE6_RESULTS.md). GitHub Actions has not run remotely.

## Phase 7 optional etcd final result

Core formatting/vet/regular/race/build checks and the actual SQLite Compose lifecycle
passed before optional work began. Added the official v3.6.16 client, an isolated
repository implementation and a real three-member local etcd deployment/profile.
The final failover test, run with race detection, abruptly killed the leader and
verified metadata continuity, concurrent-client consistency, new video uploads and
etcd-backed topology commits with two survivors. It then killed a second member
and required quorum reads/writes to fail. No media replication or unplanned media
recovery is claimed. All final regular/race/build checks passed; the default SQLite
Compose lifecycle passed again. See PHASE7_RESULTS.md and ETCD.md for full output,
setup, snapshot capacity bounds, single-coordinator restrictions and error semantics.
