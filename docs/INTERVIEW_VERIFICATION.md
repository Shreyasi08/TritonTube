# Interview verification report

Rehearsed **November 11, 2026 (America/Los_Angeles)** for the November 12 interview.
Server timestamps in UTC may show November 12 for this local-evening rehearsal.
No commits or pushes were made. Application Go source was unchanged during this
preparation; changes are documentation, ignore coverage and a read-only audit tool.

## Result summary

| Requested verification | Actual result |
| --- | --- |
| Unit/integration/race tests, vet and builds | PASS, uncached runs; details below |
| Original isolated Compose integration target | PASS, 56.148s |
| Three-member etcd leader-failure test with race detection | PASS, 65.176s |
| Main application startup | web + storage1/storage2/storage3 healthy at 8080 |
| Optional deployment startup | web + three storage + three etcd members healthy at 8081 |
| Sample generation/upload | Genuine six-second H.264/AAC MP4, 599585 bytes; HTTP returned ready |
| Complete MPEG-DASH decoding | Both video representations and audio decoded over HTTP before/after all node changes |
| Physical consistent-hash placement | All 15 file hashes/HTTP contents/owners checked independently; chunks on three nodes |
| Join fourth storage node | Four files copied/verified; independently predicted four changes, all to node4 |
| Graceful removal | Four files migrated from node2; full playback decoded after its container stopped |
| Repeatable baseline | node2 restored, node4 gracefully removed/stopped; original owners and bytes restored |
| Manual Raft commands | Actual leader etcd2 killed; probe reads/writes, application metadata and full playback passed; member restored |
| README/DESIGN review | Corrected selected-backend/transaction/CI wording; architecture and limitations reviewed |
| Ignore coverage | Media, SQLite variants/journals, local generated data, secret directories/keys/cert bundles verified |
| Git | 0 tracked files, no local commit, 59 untracked files; no push |
| Visual browser playback | **Not verified**: browser host unavailable and macOS denied native capture |

The actual prepared video is `10eb0075006d371d9737e1d6cb67655f`:
[open prepared player](http://127.0.0.1:8080/watch/10eb0075006d371d9737e1d6cb67655f).

## Current live state

The main demo and separate etcd deployment are deliberately left running and warm.
All **eleven expected running services** passed Docker and HTTP health checks.
Main storage4 is intentionally stopped after restoring the three-node baseline.
Volumes and sample media are retained for tomorrow. No volume reset was performed.

| Deployment | URL | Healthy services |
| --- | --- | --- |
| SQLite/default project | http://127.0.0.1:8080 | web, storage1, storage2, storage3 |
| Independent etcd project | http://127.0.0.1:8081 | web, storage1, storage2, storage3, etcd1, etcd2, etcd3 |

Both `/healthz` endpoints returned `status: ok`, with all three committed storage
nodes healthy. The saved local snapshot is `data/interview-services.json` (ignored).
Stop with `make down` and `make etcd-down` if desired; both preserve volumes.

## Playback evidence and the remaining browser check

The generated source had H.264 640×360 video and AAC audio, duration 6.000000s.
HTTP upload returned `status: ready`. FFmpeg successfully decoded the **complete**
DASH clip, explicitly selecting both 360p/720p representations and optional audio,
with `-xerror` and exit code 0:

```sh
./scripts/generate-sample.sh data/interview-sample.mp4
curl --fail-with-body -sS -F 'title=TritonTube Interview Demo' \
  -F 'file=@data/interview-sample.mp4' http://127.0.0.1:8080/api/videos
ffmpeg -v error -xerror \
  -i http://127.0.0.1:8080/stream/10eb0075006d371d9737e1d6cb67655f/manifest.mpd \
  -map 0:v:0 -map 0:v:1 -map '0:a?' -f null -
```

The player page returned 200, the pinned dash.js 5.2.1 CDN URL returned 200, and
`node --check` passed for `web/static/library.js` and `web/static/player.js`.
This establishes actual decodable HTTP media, not an observed browser session.

Browser automation reported `The browser access host is unavailable`. The native
Chrome fallback reported macOS ScreenCaptureKit error -3801: capture permission was
declined. No host privacy permissions were changed. **Open the prepared URL and
personally press Play, check sound/seeking and resize before the interview.**
Do not describe browser rendering or adaptive switching as visually verified here.

## Physical placement and migration evidence

The new `scripts/inspect-distribution.py` is a read-only Compose audit. It lists
actual node files, computes SHA-256 of each HTTP response and independently applies
the documented SHA-256/uint64 ring. Baseline comparison verifies every key/hash
survives migration. It does not rely solely on metadata's claimed owner or buffered
browser playback. It was exercised at every checkpoint below.

| Checkpoint | File counts by committed node | Media chunk counts | Result |
| --- | --- | --- | --- |
| before | node1=3, node2=6, node3=6 | node1=1, node2=4, node3=5 | Ring/HTTP hashes verified; 15 files |
| after-add | node1=2, node2=4, node3=5, node4=4 | node1=1, node2=3, node3=4, node4=2 | Ring/HTTP hashes verified; 15 files |
| after-remove | node1=2, node3=9, node4=4 | node1=1, node3=7, node4=2 | Ring/HTTP hashes verified; 15 files |
| baseline-restored | node1=3, node2=6, node3=6 | node1=1, node2=4, node3=5 | Ring/HTTP hashes verified; 15 files |

The add operation committed `affected: 4, copied: 4`, with no cleanup warnings.
Exactly the four independently predicted keys changed owner, all to node4. The
node2 removal committed `affected: 4, copied: 4`, also with no cleanup warnings;
its old video directory contained no files before the process was stopped.
Every baseline key and SHA-256 remained unchanged. Restoring node2 and gracefully
removing node4 restored the original physical owners and content hashes.

Read-only replay of the final state:

```sh
DEMO_VIDEO_ID=$(jq -r .id data/interview-upload.json)
python3 scripts/inspect-distribution.py "$DEMO_VIDEO_ID" \
  --baseline data/interview-distribution-before.json --plan-add node4
```

## Actual test commands and captured outputs

Go is absent from the original PATH; `/tmp/tritontube-go-env` supplies the verified
isolated Go 1.27.4 toolchain and caches. The wrapper is temporary, so install Go
normally ahead of the interview if a reboot/cleanup removes it. Docker-based live
demos and the manual Raft commands do not need host Go; Go/race tests do.

### Full uncached unit/integration/race suite, formatting, vet and binary builds

```sh
/tmp/tritontube-go-env make check
```

```text
go vet ./...
go test -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	0.449s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	0.380s
ok  	github.com/Shreyasi08/TritonTube/internal/api	0.363s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	1.036s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	0.250s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	0.444s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore	0.618s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	0.778s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	9.746s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
go test -race -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	1.296s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	1.441s
ok  	github.com/Shreyasi08/TritonTube/internal/api	1.398s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	1.702s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	1.252s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	1.438s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore	1.443s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	1.445s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	11.309s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
mkdir -p bin
go build -trimpath -o bin/web ./cmd/web
go build -trimpath -o bin/storage ./cmd/storage
go build -trimpath -o bin/admin ./cmd/admin
```

### Isolated core Docker image/lifecycle integration

```sh
/tmp/tritontube-go-env make test-compose
```

```text
TRITONTUBE_COMPOSE_TEST=1 go test -count=1 -v ./tests -run '^TestDockerComposeLifecycle$' -timeout 10m
=== RUN   TestDockerComposeLifecycle
    compose_test.go:76: isolated Compose project: tritontube-test-10ffbd88a284
    compose_test.go:112: Compose initial playback: HTTP DASH decoded both video representations and audio
    compose_test.go:139: Compose playback after add: HTTP DASH decoded both video representations and audio
    compose_test.go:151: web restart address: http://127.0.0.1:55017 → http://127.0.0.1:55019
    compose_test.go:158: Compose playback after web restart: HTTP DASH decoded both video representations and audio
    compose_test.go:168: Compose playback after removal and node shutdown: HTTP DASH decoded both video representations and audio
    compose_test.go:183: Compose upload/playback, 2-file join, four-node restart, 2-file graceful removal and Docker health checks passed
--- PASS: TestDockerComposeLifecycle (55.66s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	56.148s
```

### Real three-member etcd failover, with race detection

```sh
/tmp/tritontube-go-env make test-etcd
```

```text
TRITONTUBE_ETCD_TEST=1 go test -race -count=1 -v ./tests -run '^TestEtcdThreeMemberFailover$' -timeout 12m
=== RUN   TestEtcdThreeMemberFailover
    etcd_test.go:87: isolated etcd project: tritontube-etcd-test-c002091537ee
    etcd_test.go:132: etcd before member failure: HTTP DASH decoded both video representations and audio
    etcd_test.go:155: abruptly killed leader etcd3; two of three etcd members remain
{"level":"warn","ts":"2026-11-11T19:18:55.962593-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc00002cb40/127.0.0.1:55015","peer":"Peer{Addr: '127.0.0.1:55014', LocalAddr: '127.0.0.1:52336', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = Unavailable desc = etcdserver: leader changed"}
    etcd_test.go:224: etcd after one member failure (all media nodes still running): HTTP DASH decoded both video representations and audio
    etcd_test.go:233: new video uploaded with two-member etcd quorum: HTTP DASH decoded both video representations and audio
    etcd_test.go:249: etcd-backed storage join while one etcd member is down: HTTP DASH decoded both video representations and audio
    etcd_test.go:254: etcd-backed graceful storage removal (no media crash simulated): HTTP DASH decoded both video representations and audio
    etcd_test.go:255: metadata video, inventory, membership, concurrent CAS updates, new uploads and topology commits continued after leader failure
{"level":"warn","ts":"2026-11-11T19:19:06.122265-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc00002cb40/127.0.0.1:55015","peer":"Peer{Addr: '127.0.0.1:55014', LocalAddr: '127.0.0.1:52336', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded"}
{"level":"warn","ts":"2026-11-11T19:19:07.624869-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc0005061e0/127.0.0.1:55015","peer":"Peer{Addr: '127.0.0.1:55014', LocalAddr: '127.0.0.1:52329', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded"}
    etcd_test.go:271: two failed members: quorum reads/writes correctly failed; no unplanned media-node recovery or replication claimed
--- PASS: TestEtcdThreeMemberFailover (63.49s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	65.176s
```

All three commands exited 0. The ordinary suite skips the opt-in Docker tests;
both opt-in targets were explicitly run separately. FFmpeg was present, so the
media cases ran. Additional commands completed:

| Command | Actual result |
| --- | --- |
| `/tmp/tritontube-go-env go build ./...` | Exit 0, no output |
| `/tmp/tritontube-go-env go mod verify` | `all modules verified` |
| `node --check web/static/library.js` | Exit 0, no output |
| `node --check web/static/player.js` | Exit 0, no output |
| `sh -n` for each `scripts/*.sh` | Exit 0, no output |

The etcd test abruptly killed its identified leader etcd3; two survivors retained
video/inventory/membership records, accepted new metadata and uploads, and committed
storage topology changes. Losing a second member correctly caused deadline errors,
which are the expected negative test. Native warnings in the output correspond to
injected leader/quorum loss, not failed assertions. The test cleaned up its own
isolated resources. No media crash-recovery or replication was claimed.

### Manual Raft rehearsal for the five-minute script

The exact commands in INTERVIEW_DEMO.md were also exercised against the prewarmed
independent deployment. It identified leader etcd2, killed it with SIGKILL, read
`before-failure`, wrote `after-failure`, retrieved existing application metadata and
decoded the full existing video over HTTP. The original member was restarted with
its persisted volume; all three native etcd endpoint-health checks succeeded.
Captured output:

```text
ACTUAL LEADER etcd2; COMMAND SURVIVOR etcd1
OK
 Container tritontube-etcd-etcd2-1 Killing 
 Container tritontube-etcd-etcd2-1 Killed 
{"level":"warn","ts":"2026-11-12T03:30:51.757468Z","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0x2f97454641e0/etcd1:2379","peer":"Peer{Addr: '172.20.0.3:2379', LocalAddr: '172.20.0.7:56736', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = Unavailable desc = etcdserver: leader changed"}
/interview/demo-probe
before-failure
OK
{
  "id": "ca9c8f7c2a2bb84e1834006daf429e75",
  "status": "ready",
  "title": "TritonTube etcd Interview Demo"
}
MANUAL ETCD FAILURE: existing application metadata and complete HTTP DASH playback passed
 Container tritontube-etcd-etcd2-1 Starting 
 Container tritontube-etcd-etcd2-1 Started 
 Container tritontube-etcd-etcd2-1 Waiting 
 Container tritontube-etcd-etcd2-1 Healthy 
http://etcd1:2379 is healthy: successfully committed proposal: took = 3.526292ms
http://etcd3:2379 is healthy: successfully committed proposal: took = 3.585791ms
http://etcd2:2379 is healthy: successfully committed proposal: took = 3.625458ms
{"nodes":[{"node":{"id":"node1","address":"storage1:9001"},"healthy":true},{"node":{"id":"node2","address":"storage2:9001"},"healthy":true},{"node":{"id":"node3","address":"storage3:9001"},"healthy":true}],"service":"TritonTube","status":"ok"}
NAME                         IMAGE                                  COMMAND                  SERVICE    CREATED         STATUS                   PORTS
tritontube-etcd-etcd1-1      gcr.io/etcd-development/etcd:v3.6.16   "/usr/local/bin/etcd"    etcd1      4 minutes ago   Up 4 minutes (healthy)   127.0.0.1:2379->2379/tcp
tritontube-etcd-etcd2-1      gcr.io/etcd-development/etcd:v3.6.16   "/usr/local/bin/etcd"    etcd2      4 minutes ago   Up 5 seconds (healthy)   127.0.0.1:2381->2379/tcp
tritontube-etcd-etcd3-1      gcr.io/etcd-development/etcd:v3.6.16   "/usr/local/bin/etcd"    etcd3      4 minutes ago   Up 4 minutes (healthy)   127.0.0.1:2383->2379/tcp
tritontube-etcd-storage1-1   tritontube:local                       "storage -listen :90…"   storage1   4 minutes ago   Up 4 minutes (healthy)   8080/tcp, 9001/tcp
tritontube-etcd-storage2-1   tritontube:local                       "storage -listen :90…"   storage2   4 minutes ago   Up 4 minutes (healthy)   8080/tcp, 9001/tcp
tritontube-etcd-storage3-1   tritontube:local                       "storage -listen :90…"   storage3   4 minutes ago   Up 4 minutes (healthy)   8080/tcp, 9001/tcp
tritontube-etcd-web-1        tritontube:local                       "web -listen :8080 -…"   web        4 minutes ago   Up 4 minutes (healthy)   127.0.0.1:8081->8080/tcp
```

## Documentation, ignore checks and problems corrected

Created INTERVIEW_DEMO.md with exact startup/prewarm/recovery commands, a timed
five-minute script, architecture and hash/migration/gRPC/Raft explanations, exactly
five challenging Q&As, and explicit production improvements. README now links it
and the physical audit tool. README/DESIGN wording now covers either metadata
backend, native etcd transaction publication, indeterminate outcomes and all three
CI jobs accurately. No remote GitHub Actions run is claimed.

Ignore coverage was expanded for SQLite alternative extensions and rollback
journals, generated DASH files, certificate bundles and Python cache. Representative
`git check-ignore` checks passed for:

```text
data/interview-sample.mp4
data/interview-upload.json
metadata.db / metadata.db-shm / metadata.db-wal / metadata.db-journal
metadata.sqlite / metadata.sqlite3 / metadata.sqlite3-wal
sample.m4s / sample.mpd
.env / .env.local / secrets/demo.json / .aws/credentials / private.key / certificate.p12
```

`.env.example` remains allowed. This verifies exclusion rules, not a claim of a
complete secret-content/security audit. Sample media, upload receipts, audit JSON
and named-volume databases are not included in the source Git status.

One initial ignore-check command accidentally used zsh's reserved `path` variable,
which affected only that subprocess's PATH. It was rerun with a task-specific
variable and all assertions passed. No shell profile or persistent PATH was changed.
No application defect was found in the new rehearsal. The manual-browser blocker
is unresolved and explicitly distinguished from successful media decoding.

## Git status: not clean, no local commit

`git ls-files` returned zero tracked files. `git rev-parse --verify HEAD` exited
128 (`fatal: Needed a single revision`), confirming there is no local commit.
All source/configuration/documentation files are currently untracked; nothing was
staged, committed or pushed. The remote remains
`https://github.com/Shreyasi08/TritonTube.git`.

Actual `git status --short --untracked-files=all` snapshot:

```text
?? .dockerignore
?? .github/workflows/go.yml
?? .gitignore
?? DESIGN.md
?? Dockerfile
?? INTERVIEW_DEMO.md
?? Makefile
?? README.md
?? cmd/admin/main.go
?? cmd/admin/main_test.go
?? cmd/storage/main.go
?? cmd/web/main.go
?? cmd/web/main_test.go
?? docker-compose.etcd.yml
?? docker-compose.yml
?? docs/ETCD.md
?? docs/IMPLEMENTATION_PLAN.md
?? docs/INTERVIEW_VERIFICATION.md
?? docs/PHASE6_RESULTS.md
?? docs/PHASE7_RESULTS.md
?? docs/VALIDATION.md
?? go.mod
?? go.sum
?? internal/api/cluster.go
?? internal/api/server.go
?? internal/api/server_test.go
?? internal/api/ui.go
?? internal/cluster/migration.go
?? internal/cluster/router.go
?? internal/cluster/router_test.go
?? internal/hashring/ring.go
?? internal/hashring/ring_test.go
?? internal/metadata/etcdstore/store.go
?? internal/metadata/etcdstore/store_test.go
?? internal/metadata/repository.go
?? internal/metadata/sqlite.go
?? internal/metadata/sqlite_test.go
?? internal/observability/log.go
?? internal/storage/client.go
?? internal/storage/identity.go
?? internal/storage/server.go
?? internal/storage/server_test.go
?? internal/transcode/ffmpeg.go
?? proto/storage/v1/storage.pb.go
?? proto/storage/v1/storage.proto
?? proto/storage/v1/storage_grpc.pb.go
?? scripts/generate-sample.sh
?? scripts/inspect-distribution.py
?? scripts/test-e2e.sh
?? tests/compose_test.go
?? tests/etcd_test.go
?? tests/integration_test.go
?? tests/phase5_test.go
?? web/assets.go
?? web/static/library.js
?? web/static/player.js
?? web/static/style.css
?? web/templates/library.html
?? web/templates/watch.html
```

Review/stage and make the initial commit when ready; the no-push instruction was
preserved. Do not use `git clean` or delete volumes as a demo reset.

## Limits to state in the interview

Media is unreplicated; unplanned sole-owner failure loses file availability. SQLite
is single-host. Etcd replicates metadata only, uses a 1 MiB CAS snapshot with bounded
operations and still supports one coordinator. There is no coordinator lease,
auth/TLS, automatic media repair, job queue or orphan reconciliation. File/HTTP
spooling uses disk, viewer concurrency is not globally bounded, and graceful
migration pauses file routing. The local etcd members share one host, so no host-
failure or five-member/two-failure survival claim is made. Browser visual playback
and remote CI are not verified. See INTERVIEW_DEMO.md and DESIGN.md for realistic
production changes and precise failure semantics.
