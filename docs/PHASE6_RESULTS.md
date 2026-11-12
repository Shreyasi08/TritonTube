# Phase 6 execution report

This is the historical Phase 6 report. The optional etcd backend was subsequently
implemented and tested; see [Phase 7 results](PHASE7_RESULTS.md).

Executed November 11, 2026 (America/Los_Angeles). Local host: macOS arm64,
Go 1.27.4, FFmpeg 7.1.1, protoc 3.20.3. Docker Desktop was started successfully;
Docker Engine 29.6.1 ran Linux aarch64 containers. The original PATH had no Go;
`/tmp/tritontube-go-env` supplies the isolated toolchain and caches.

## Delivered

- Multi-stage Docker image and Compose services: web, three independent storage
  volumes/processes, optional fourth-node profile, loopback HTTP publication,
  non-root runtime and health/startup dependencies.
- Web health probe checks `/healthz` (SQLite plus storage health), replacing the
  former storage-only container probe. Tests cover both dependency failures.
- Make targets for protobuf generation, formatting, vet, regular/race tests,
  generated-media E2E tests, opt-in Compose tests, builds and demo lifecycle.
- Two GitHub Actions jobs: Go validation and actual container lifecycle testing.
- Architecture, migration consistency, failure boundaries and limitations in
  README.md/DESIGN.md. Ordinary `down`/`clean` retain application data.

Every command below completed with **exit code 0**. Captured output is included
verbatim; silent successful checks are identified explicitly.

## Protobuf regeneration

```sh
/tmp/tritontube-go-env make proto
```

```text
GOBIN=/Users/shreyasinath/Documents/Projects/TritonTube/.tools/bin go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN=/Users/shreyasinath/Documents/Projects/TritonTube/.tools/bin go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
PATH="/Users/shreyasinath/Documents/Projects/TritonTube/.tools/bin:$PATH" protoc --go_out=. --go_opt=module=github.com/Shreyasi08/TritonTube --go-grpc_out=. --go-grpc_opt=module=github.com/Shreyasi08/TritonTube proto/storage/v1/storage.proto
```

## Formatting, vet, complete regular/race suites and executable builds

```sh
/tmp/tritontube-go-env make check
```

```text
go vet ./...
go test -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	0.302s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	0.450s
ok  	github.com/Shreyasi08/TritonTube/internal/api	0.321s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	0.579s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	0.203s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	0.363s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	0.412s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	8.323s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
go test -race -count=1 ./...
ok  	github.com/Shreyasi08/TritonTube/cmd/admin	1.285s
?   	github.com/Shreyasi08/TritonTube/cmd/storage	[no test files]
ok  	github.com/Shreyasi08/TritonTube/cmd/web	1.374s
ok  	github.com/Shreyasi08/TritonTube/internal/api	1.566s
ok  	github.com/Shreyasi08/TritonTube/internal/cluster	1.695s
ok  	github.com/Shreyasi08/TritonTube/internal/hashring	1.219s
ok  	github.com/Shreyasi08/TritonTube/internal/metadata	1.590s
?   	github.com/Shreyasi08/TritonTube/internal/observability	[no test files]
ok  	github.com/Shreyasi08/TritonTube/internal/storage	1.405s
?   	github.com/Shreyasi08/TritonTube/internal/transcode	[no test files]
?   	github.com/Shreyasi08/TritonTube/proto/storage/v1	[no test files]
ok  	github.com/Shreyasi08/TritonTube/tests	10.829s
?   	github.com/Shreyasi08/TritonTube/web	[no test files]
mkdir -p bin
go build -trimpath -o bin/web ./cmd/web
go build -trimpath -o bin/storage ./cmd/storage
go build -trimpath -o bin/admin ./cmd/admin
```

## Generated-media and compiled CLI end-to-end tests

```sh
/tmp/tritontube-go-env make test-e2e
```

```text
GO="go" ./scripts/test-e2e.sh
=== RUN   TestDASHEndToEnd
=== RUN   TestDASHEndToEnd/audio=false
    integration_test.go:341: MP4 92289 bytes → SQLite ready record → 8 inventoried files across 3 nodes → HTTP DASH decoded (360p, 720p, audio=false)
=== RUN   TestDASHEndToEnd/audio=true
    integration_test.go:341: MP4 121413 bytes → SQLite ready record → 11 inventoried files across 3 nodes → HTTP DASH decoded (360p, 720p, audio=true)
--- PASS: TestDASHEndToEnd (1.95s)
    --- PASS: TestDASHEndToEnd/audio=false (1.03s)
    --- PASS: TestDASHEndToEnd/audio=true (0.93s)
=== RUN   TestAdminNodeChangesKeepDASHPlayable
    phase5_test.go:129: before node changes: HTTP DASH decoded both video representations and audio
    phase5_test.go:157: after CLI add: HTTP DASH decoded both video representations and audio
    phase5_test.go:158: CLI add: 4 files copied and verified before membership commit
    phase5_test.go:176: after CLI remove and node shutdown: HTTP DASH decoded both video representations and audio
    phase5_test.go:177: CLI remove node2: 3 files retained on verified destinations
    phase5_test.go:205: after coordinator reconstruction: HTTP DASH decoded both video representations and audio
--- PASS: TestAdminNodeChangesKeepDASHPlayable (2.25s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	4.499s
```

## Real Docker image/container lifecycle test

```sh
/tmp/tritontube-go-env make test-compose
```

```text
TRITONTUBE_COMPOSE_TEST=1 go test -count=1 -v ./tests -run '^TestDockerComposeLifecycle$' -timeout 10m
=== RUN   TestDockerComposeLifecycle
    compose_test.go:76: isolated Compose project: tritontube-test-221bc082b6c8
    compose_test.go:112: Compose initial playback: HTTP DASH decoded both video representations and audio
    compose_test.go:139: Compose playback after add: HTTP DASH decoded both video representations and audio
    compose_test.go:151: web restart address: http://127.0.0.1:55004 → http://127.0.0.1:55006
    compose_test.go:158: Compose playback after web restart: HTTP DASH decoded both video representations and audio
    compose_test.go:168: Compose playback after removal and node shutdown: HTTP DASH decoded both video representations and audio
    compose_test.go:183: Compose upload/playback, 4-file join, four-node restart, 4-file graceful removal and Docker health checks passed
--- PASS: TestDockerComposeLifecycle (46.09s)
PASS
ok  	github.com/Shreyasi08/TritonTube/tests	46.552s
```

## Additional checks

| Exact command | Actual output / result |
| --- | --- |
| `/tmp/tritontube-go-env go build ./...` | No output; exit 0 |
| `/tmp/tritontube-go-env go mod verify` | `all modules verified` |
| `docker compose --profile scale config --quiet` | No output; exit 0 |
| `/tmp/tritontube-go-env gofmt -l cmd internal proto tests web` | No output; formatting clean |
| `docker info --format 'Engine={{.ServerVersion}} OS={{.OSType}} Arch={{.Architecture}}'` | `Engine=29.6.1 OS=linux Arch=aarch64` |

After the successful Compose run, Docker container and volume queries filtered to
project `tritontube-test-221bc082b6c8` returned no entries. The test's own image was
also removed by its successful deferred Compose cleanup. Existing demo resources
were isolated by a different project, image tag, volumes and published port.

## Failures found and fixed before the passing run

The first container run passed image build, health/startup, MP4 upload and playback
after a node joined, but the test kept the old dynamic host port after restarting
web. Docker assigned a new port. The test now rediscovers it. A subsequent run
exposed a second harness race: HTTP was ready while Docker's asynchronous health
status still said `starting`. The test now uses Compose `--wait` after restart.
The final run above passed all stages, with four files moved on both join/removal.
No application migration or playback failure remained.

## What these results establish

The non-root containers actually wrote SQLite and media to their separate named
volumes, transcoded an MP4, streamed decodable 360p/720p DASH plus audio, joined a
fourth node, recovered four-node membership across a web-process restart, and
retained playback after graceful removal and shutdown of that storage container.
Docker health status was healthy for web and all remaining storage containers.
The full Go suite also exercised corrupt/missing media, metadata failures, path
confinement, concurrent uploads and serialized topology changes. Race detection
reported no races.

## Scope and remaining limits

The regular/race Go suites intentionally skip the opt-in Docker test; it was run
separately and passed here. FFmpeg was installed, so media tests executed rather
than being skipped. GitHub Actions is configured but has not been pushed or run
remotely. Visual browser playback remains unverified; successful playback claims
here refer to actual FFmpeg decoding through the HTTP DASH endpoints.

This remains a local demo with one SQLite coordinator, no metadata HA, no media
replication/crash recovery, no authentication/TLS, bounded synchronous processing,
disk-backed response spooling and no automatic orphan reconciliation. Health
probes are availability checks, not integrity/capacity guarantees. Hard termination
can leave temporary files or bypass test cleanup. Optional etcd is not implemented.
See DESIGN.md and README.md for exact consistency and operational limitations.
No generated media, local databases, test images or test volumes were committed.
No commits or pushes were made.
