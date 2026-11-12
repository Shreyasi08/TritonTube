# TritonTube implementation plan

Project and GitHub repository: **TritonTube**. Module: `github.com/Shreyasi08/TritonTube`.
All implementation is original; reference repositories are conceptual references only.

## Architecture and scope

Go HTTP frontend → streaming gRPC filesystem storage on three independent nodes.
SHA-256 consistent hashing uses uint64 positions, sorted virtual nodes (default 100),
binary-search lookup and deterministic collision ordering. Each file uses the key
`videoID/relativeFilename`. SQLite stores video state, inventory and ownership behind
a repository interface. FFmpeg produces MPEG-DASH for dash.js browser playback.
An admin CLI manages health, membership and verified graceful redistribution.
Optional native-client etcd was implemented only after the complete SQLite path passed.

## Phases and gates

1. Go module, protobuf contract, storage process, bounded streaming upload/download,
   inventory/delete, atomic publication, checksum verification and path validation.
2. Consistent hash ring and distributed file routing, including deterministic lookup,
   wraparound, collision ordering and bounded-remapping tests.
3. SQLite repository, health/library/metadata APIs and bounded MP4 upload.
4. FFmpeg DASH pipeline, cleanup on failure, responsive library/upload UI and player.
5. Admin CLI and serialized graceful membership migration: copy, verify, commit routing,
   then delete sources; explicit failed status and no premature deletion.
6. Integration tests, Compose with three nodes, Make targets, demo media generator,
   GitHub Actions, README and DESIGN documentation.
7. Optional etcd backend and actual failover testing only after phases 1–6 pass.

After **each** phase: gofmt, `go test ./...`, `go build ./...`, fix failures and
report actual results before proceeding. Run race detection and end-to-end tests.
Do not commit without review; never push without request.

## Required validation

- Hash determinism, ownership, wraparound and limited remapping.
- Multi-node streaming upload/download, missing-file and invalid-path errors.
- MP4 upload → FFmpeg → retrievable manifest and segments.
- Add and graceful removal retain accessible content and correct ownership.
- Migration failure retains source files and existing routing.
- Concurrent uploads and race detection.
- Optional etcd failover only if implemented and exercised.

## Explicit limits

No authentication for the local demo. SQLite is a single-host metadata dependency,
not a distributed or fault-tolerant database. Graceful membership changes require
reachable sources. Without replication, unplanned node loss is not automatically
recoverable. Persist full inventory and ownership; publish new topology only after
all affected files are safely copied and verified. Bound uploads and RPC messages,
propagate cancellation, log failures and remove temporary media on errors.

## Environment inspection

Workspace initially contains only `.git`; no existing code or documentation.
Remote: `https://github.com/Shreyasi08/TritonTube.git`. Git and protoc 3.20.3 are
available. FFmpeg 7.1.1 is installed. Docker CLI is installed but its daemon is not
running. Go is absent from PATH; use an isolated verified toolchain under `/tmp`.
Git author identity is not configured; no commits are planned.

## Completion status

Phases **1–6 are implemented** and passed their Go formatting/test/build gates.
Regular/race tests, vet and the compiled multi-process CLI/media smoke test passed.
See [VALIDATION.md](VALIDATION.md) for exact outcomes and remaining verification
limits. Docker runtime testing now passes after starting Docker Desktop; visual
browser verification remains limited by unavailable browser access. Optional phase 7 now implements an isolated etcd backend and three-member
metadata continuity testing; see ETCD.md and PHASE7_RESULTS.md. Media files remain
unreplicated and have no unplanned storage-node recovery guarantee.

### Phase 3–4 focused verification

SQLite/upload failure handling and FFmpeg output validation/cleanup were strengthened.
Each phase passed its full Go test/build gate; final vet/race tests passed as well.
`make test-e2e` now reproduces the generated-MP4 → SQLite → three-node gRPC storage
→ HTTP MPEG-DASH decoding test, with both 360p/720p video and optional audio.
Library/player routes and safe title rendering are covered. See VALIDATION.md for
actual media sizes/results and the remaining visual browser-access limitation.

### Phase 5 focused verification

List/add/graceful-remove commands and checksum-verified old/new-ring redistribution
are implemented and tested. The generated-video test now invokes the compiled admin
CLI and decodes HTTP DASH before changes, after add, after removal and node shutdown,
and after coordinator reconstruction from persisted membership. Corrupt source,
corrupt destination read-back, failed metadata commits and concurrent changes retain
old routing and source copies. Persistent storage identities reject address aliases
before copying to prevent unsafe source/destination cleanup. Full tests, build,
vet and race detection passed; `make test-e2e` includes the CLI/media migration case.

### Phase 6 completion

Docker Compose now has a SQLite-aware HTTP web health check and opt-in, isolated
container lifecycle testing. GitHub Actions defines separate Go and Docker jobs.
`make check`, `make test-e2e`, `make test-compose`, protobuf regeneration, full
build and module verification passed locally. The real container test verified
upload/transcode/playback, node join, persisted membership after restart, graceful
removal and playback after stopping the departed node. Container test port/health
restart races were fixed before the final passing run. See PHASE6_RESULTS.md for
exact commands and captured outputs; Docker's earlier environment blocker is resolved.

### Phase 7 optional etcd backend

Core regular/race/build checks and the real SQLite Compose lifecycle passed before
optional work began. The official etcd v3.6.16 client now implements the repository
using bounded, revision-checked metadata snapshots. The optional Compose overlay
starts a real three-member etcd cluster with separate volumes. A leader SIGKILL
continuity test verifies existing/new metadata and video uploads with two survivors,
then requires errors after losing quorum. It also tests atomic topology updates,
concurrent clients and indeterminate-commit fencing. Storage files remain singly
owned; no media crash-recovery or five-member deployment claim is made.
