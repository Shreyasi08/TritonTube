# Phase 7 optional etcd execution report

Executed November 11, 2026 (America/Los_Angeles). Optional implementation began
only after both the full core Go gate and real SQLite Compose lifecycle passed.
The official etcd client/server version is pinned to v3.6.16. Local Go is 1.27.4;
Docker ran actual Linux containers, not an embedded/fake etcd API. All commands
below completed with exit code 0. The temporary wrapper supplies the Go toolchain.

Implemented an independent etcd metadata repository, backend-selection flags, a
three-member local Compose overlay/profile, native revision-CAS state updates,
indeterminate topology-commit fencing, and an opt-in failover test/CI job. SQLite
remains the default and received its own post-change Docker regression run.

**No storage-file fault tolerance is claimed.** Media remains unreplicated. The
failure injected below is an etcd leader process failure. The extra media process
is stopped only after a successful graceful removal; no unplanned media crash or
recovery is exercised or promised.

## Core gate before optional implementation

```sh
/tmp/tritontube-go-env make check
```

```text
go vet ./...
go test -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	0.255s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	0.234s
ok  	github.com/Shreyasi08/TritonTube/internal/api	0.222s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	0.487s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	0.112s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	0.252s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	0.299s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	8.185s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
go test -race -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	1.312s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	1.297s
ok  	github.com/Shreyasi08/TritonTube/internal/api	1.299s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	1.617s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	1.170s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	1.339s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	1.341s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	11.103s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
mkdir -p bin
go build -trimpath -o bin/web ./cmd/web
go build -trimpath -o bin/storage ./cmd/storage
go build -trimpath -o bin/admin ./cmd/admin
```

## Core Docker lifecycle before optional implementation

```sh
/tmp/tritontube-go-env make test-compose
```

```text
TRITONTUBE_COMPOSE_TEST=1 go test -count=1 -v ./tests -run '^TestDockerComposeLifecycle$' -timeout 10m
=== RUN   TestDockerComposeLifecycle
    compose_test.go:76: isolated Compose project: tritontube-test-0ff060b553d9
    compose_test.go:112: Compose initial playback: HTTP DASH decoded both video representations and audio
    compose_test.go:139: Compose playback after add: HTTP DASH decoded both video representations and audio
    compose_test.go:151: web restart address: http://127.0.0.1:55000 → http://127.0.0.1:55002
    compose_test.go:158: Compose playback after web restart: HTTP DASH decoded both video representations and audio
    compose_test.go:168: Compose playback after removal and node shutdown: HTTP DASH decoded both video representations and audio
    compose_test.go:183: Compose upload/playback, 5-file join, four-node restart, 5-file graceful removal and Docker health checks passed
--- PASS: TestDockerComposeLifecycle (47.47s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	47.900s
```

## Final regular/race suites, vet and builds

```sh
/tmp/tritontube-go-env make check
```

```text
go vet ./...
go test -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	0.323s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	0.302s
ok  	github.com/Shreyasi08/TritonTube/internal/api	0.377s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	0.613s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	0.144s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	0.275s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore	0.415s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	0.298s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	9.254s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
go test -race -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	1.355s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	1.408s
ok  	github.com/Shreyasi08/TritonTube/internal/api	1.355s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	1.688s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	1.220s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	1.410s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata/etcdstore	1.414s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	1.411s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	13.767s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
mkdir -p bin
go build -trimpath -o bin/web ./cmd/web
go build -trimpath -o bin/storage ./cmd/storage
go build -trimpath -o bin/admin ./cmd/admin
```

## Real three-member etcd test with race detection

```sh
/tmp/tritontube-go-env make test-etcd
```

```text
TRITONTUBE_ETCD_TEST=1 go test -race -count=1 -v ./tests -run '^TestEtcdThreeMemberFailover$' -timeout 12m
=== RUN   TestEtcdThreeMemberFailover
    etcd_test.go:87: isolated etcd project: tritontube-etcd-test-1fc05123f09a
    etcd_test.go:132: etcd before member failure: HTTP DASH decoded both video representations and audio
    etcd_test.go:155: abruptly killed leader etcd3; two of three etcd members remain
{"level":"warn","ts":"2026-11-11T19:06:11.581870-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc0005b1a40/127.0.0.1:55007","peer":"Peer{Addr: '127.0.0.1:55009', LocalAddr: '127.0.0.1:51410', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = Unavailable desc = etcdserver: leader changed"}
    etcd_test.go:224: etcd after one member failure (all media nodes still running): HTTP DASH decoded both video representations and audio
    etcd_test.go:233: new video uploaded with two-member etcd quorum: HTTP DASH decoded both video representations and audio
    etcd_test.go:249: etcd-backed storage join while one etcd member is down: HTTP DASH decoded both video representations and audio
    etcd_test.go:254: etcd-backed graceful storage removal (no media crash simulated): HTTP DASH decoded both video representations and audio
    etcd_test.go:255: metadata video, inventory, membership, concurrent CAS updates, new uploads and topology commits continued after leader failure
{"level":"warn","ts":"2026-11-11T19:06:21.002758-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc0005b1a40/127.0.0.1:55007","peer":"Peer{Addr: '127.0.0.1:55009', LocalAddr: '127.0.0.1:51410', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded"}
{"level":"warn","ts":"2026-11-11T19:06:22.504970-0800","logger":"etcd-client","caller":"v3@v3.6.16/retry_interceptor.go:68","msg":"retrying of unary invoker failed","target":"etcd-endpoints://0xc0001d43c0/127.0.0.1:55007","peer":"Peer{Addr: '127.0.0.1:55009', LocalAddr: '127.0.0.1:51402', AuthInfo: 'insecure'}","method":"/etcdserverpb.KV/Range","attempt":0,"error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded"}
    etcd_test.go:271: two failed members: quorum reads/writes correctly failed; no unplanned media-node recovery or replication claimed
--- PASS: TestEtcdThreeMemberFailover (56.32s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	57.744s
```

## Default SQLite deployment regression after optional work

```sh
/tmp/tritontube-go-env make test-compose
```

```text
TRITONTUBE_COMPOSE_TEST=1 go test -count=1 -v ./tests -run '^TestDockerComposeLifecycle$' -timeout 10m
=== RUN   TestDockerComposeLifecycle
    compose_test.go:76: isolated Compose project: tritontube-test-7ee86cbbbdb8
    compose_test.go:112: Compose initial playback: HTTP DASH decoded both video representations and audio
    compose_test.go:139: Compose playback after add: HTTP DASH decoded both video representations and audio
    compose_test.go:151: web restart address: http://127.0.0.1:55011 → http://127.0.0.1:55012
    compose_test.go:158: Compose playback after web restart: HTTP DASH decoded both video representations and audio
    compose_test.go:168: Compose playback after removal and node shutdown: HTTP DASH decoded both video representations and audio
    compose_test.go:183: Compose upload/playback, 5-file join, four-node restart, 5-file graceful removal and Docker health checks passed
--- PASS: TestDockerComposeLifecycle (46.32s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	46.733s
```

## Additional checks and interpretation

`/tmp/tritontube-go-env go build ./...` completed with no output, exit 0.
`/tmp/tritontube-go-env go mod verify` returned `all modules verified`.
The optional overlay passed:

```sh
docker compose -f docker-compose.yml -f docker-compose.etcd.yml --profile etcd config --quiet
```

No output, exit 0. Formatting was clean. Etcd-dependent Docker tests are opt-in and
skipped by the ordinary suite; `make test-etcd` ran them explicitly with `-race`.
The original core Docker test also ran explicitly before and after implementation.
GitHub Actions now defines the etcd job, but no remote CI execution/push is claimed.

The successful final etcd run killed the identified leader `etcd3` with SIGKILL,
then verified existing video metadata, file inventory and storage membership;
new metadata writes were read through a separate survivor-only client. New actual
MP4 uploads transcoded and decoded through HTTP DASH, and storage join/graceful
removal committed through etcd while only two members remained. The backend's
contract checks also covered concurrent CAS clients, duplicate/missing records,
restart recovery, rejected stale topology updates and snapshot-size rejection.

The second failed member deliberately removed quorum. Read/write errors were the
expected negative result, not a test failure. Native client warnings about leader
change/deadlines in the captured output correspond to those injected failures.
The suite returned PASS with no race detector report. A separate lost-response
regression verified that a possibly committed topology retains sources, fences
further file writes/topology changes, preserves the actual durable commit and can
be reconstructed from authoritative metadata.

Both test deployments used unique projects, application image tags, volumes and
dynamic host ports. Normal deferred cleanup removed their resources. The shared
official etcd image was retained rather than deleting a potentially reused image.
No generated media, SQLite databases, etcd member data, commits or pushes were added
to the repository.

## Limits

See [ETCD.md](ETCD.md) for setup and exact guarantees. The demo backend rewrites
one native-CAS JSON snapshot, capped at 1,048,576 encoded bytes, with five-second
operation contexts and bounded comparison retries. It supports one web coordinator,
not automatic web failover or multiple in-memory routing authorities. The local
etcd deployment is unauthenticated/plaintext and all members share one Docker host.
The tested guarantee is metadata continuity after one member process failure in a
three-member cluster. There is no five-member/two-failure survival claim, no automatic
SQLite import, no metadata-to-media distributed transaction, no media replication,
and no unplanned storage-file recovery. Browser visual playback and remote GitHub
Actions were not observed; playback results are genuine FFmpeg HTTP DASH decoding.
