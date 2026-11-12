# TritonTube interview demo

**Interview:** November 12, 2026. **Rehearsed:** November 11, 2026, America/Los_Angeles.
Run commands from `/Users/shreyasinath/Documents/Projects/TritonTube`.

## What is verified and what remains manual

The complete six-second H.264/AAC sample was uploaded through HTTP, transcoded to
360p/720p DASH, physically distributed across three nodes and decoded over HTTP.
Adding node4 moved four files; gracefully removing node2 moved four files. Every
physical file/HTTP SHA-256 matched an independently computed ring owner before and
after changes. The original three-node baseline was restored and remains running.

The prepared clip is **10eb0075006d371d9737e1d6cb67655f**, at
**http://127.0.0.1:8080/watch/10eb0075006d371d9737e1d6cb67655f**. Its source is ignored
`data/interview-sample.mp4`; upload response and audit snapshots are in `data/`.

**Visual browser playback is not agent-verified.** Browser access was unavailable
and macOS denied native capture. The player page and pinned dash.js CDN returned
200, JavaScript syntax passed, and FFmpeg decoded both complete video representations
plus audio over HTTP. Before the interview, open the URL and personally check Play,
audio, seeking and resizing. Do not describe that manual check as completed until
it is. Adaptive quality switching was not visually measured.

See [the final verification report](docs/INTERVIEW_VERIFICATION.md) for actual
commands, outputs, service health, test results and Git status.

## Preparation outside the five-minute timer

Prerequisites: Docker Desktop/Engine, Compose v2+, host FFmpeg/ffprobe, `curl`, `jq`,
and Python 3.9+ for the independent physical-distribution audit. Host Go 1.26+ and
a C toolchain are needed for tests/race detection, not the container application.
The main demo does not need protoc. Browser playback needs internet for dash.js.

```sh
cd /Users/shreyasinath/Documents/Projects/TritonTube
docker compose up --build -d --wait
docker compose ps
curl --fail http://127.0.0.1:8080/healthz
docker compose exec -T web admin list
```

Expected baseline: healthy web + storage1/storage2/storage3, with committed IDs
node1/node2/node3. Storage4 may exist but be stopped; that is intentional. Do not
join it before the timed demonstration. Persisted membership wins over seed flags.
Do not delete volumes/databases to reset the demo.

The sample is already generated. On a fresh checkout, generate it once; the script
refuses to overwrite files:

```sh
./scripts/generate-sample.sh data/interview-sample.mp4
ffprobe -v error -show_entries format=duration:stream=codec_name,width,height \
  -of json data/interview-sample.mp4
```

If `data/interview-upload.json` is absent on a fresh checkout, preload the clip:

```sh
curl --fail-with-body -sS -F 'title=TritonTube Interview Demo' \
  -F 'file=@data/interview-sample.mp4' http://127.0.0.1:8080/api/videos \
  -o data/interview-upload.json
```

Prepare variables and an audit baseline using the already uploaded, rehearsed clip:

```sh
DEMO_VIDEO_ID=$(jq -r .id data/interview-upload.json)
python3 scripts/inspect-distribution.py "$DEMO_VIDEO_ID" --plan-add node4 \
  > data/interview-distribution-live-before.json
jq '{files_per_node,media_segments_per_node,planned_add}' \
  data/interview-distribution-live-before.json
```

The audited rehearsal clip has 15 files: node1=3, node2=6, node3=6. Its ten media
chunks are node1=1, node2=4, node3=5. Adding node4 is predicted to move four files.
These counts are specific to this video ID, 100 virtual nodes and the baseline
membership; a newly uploaded clip can have different counts.

Prestart the independent optional etcd deployment before the timer:

```sh
TRITONTUBE_HTTP_PORT=8081 make etcd-up
ec() {
  TRITONTUBE_HTTP_PORT=8081 docker compose -p tritontube-etcd \
    -f docker-compose.yml -f docker-compose.etcd.yml --profile etcd "$@"
}
INTERVIEW_ETCD_ENDPOINTS=http://etcd1:2379,http://etcd2:2379,http://etcd3:2379
ec ps
```

This is a separate project/metadata/storage set on port 8081. It never imports the
SQLite demo automatically. The rehearsal also preloaded a clip here, recorded in
`data/interview-etcd-upload.json`. On a fresh deployment, use:

```sh
curl --fail-with-body -sS -F 'title=TritonTube etcd Interview Demo' \
  -F 'file=@data/interview-sample.mp4' http://127.0.0.1:8081/api/videos \
  -o data/interview-etcd-upload.json
```

Always load the prepared video's ID, whether reusing the preloaded clip or uploading:

```sh
INTERVIEW_ETCD_VIDEO_ID=$(jq -r .id data/interview-etcd-upload.json)
```

Prepare leader/survivor variables with exact integer parsing, not assumed names:

```sh
ec exec -T etcd1 /usr/local/bin/etcdctl --endpoints="$INTERVIEW_ETCD_ENDPOINTS" \
  --write-out=json endpoint status > data/interview-etcd-status.json
INTERVIEW_ETCD_LEADER=$(python3 -c 'import json,urllib.parse; s=json.load(open("data/interview-etcd-status.json")); print(next(urllib.parse.urlparse(x["Endpoint"]).hostname for x in s if x["Status"]["header"]["member_id"]==x["Status"]["leader"]))')
INTERVIEW_ETCD_SURVIVOR=$(python3 -c 'import sys; print(next(x for x in ["etcd1","etcd2","etcd3"] if x!=sys.argv[1]))' "$INTERVIEW_ETCD_LEADER")
```

Refresh the leader status immediately before the Raft demonstration if this
preparation was done much earlier. The leader can change; do not hardcode etcd1.

## Five-minute live demonstration

Have two terminals and the library/player tab open. Build/download/start services
before the timer; do not run a cold image build during the five minutes.

| Time | Show and say |
| --- | --- |
| 0:00–0:30 | Show `docker compose ps`, `/healthz`, library and architecture. “One Go coordinator, three independent gRPC filesystem nodes, SQLite by default; optional native etcd metadata.” |
| 0:30–1:00 | Upload the sample, show `ready` and open its player. “FFmpeg generates two representations; dash.js requests MPD/init/media files through the Go API.” |
| 1:00–1:45 | Show physical distribution and four predicted moves. Start/join storage4; show committed migration and four moved files for the rehearsed clip. |
| 1:45–2:45 | Gracefully remove node2, stop it and verify all 15 rehearsed files still match and the full DASH clip decodes. Reload playback rather than relying on buffered frames. |
| 2:45–4:15 | In the prewarmed etcd terminal, kill the actual leader, read/write a probe via a survivor and retrieve application metadata. Explain two-of-three quorum, then restore the member. |
| 4:15–5:00 | Show passing test report. State limits: no media replication/unplanned file recovery, one coordinator, bounded etcd snapshot, local unauthenticated deployment. Name concrete production improvements. |

### Upload (0:30)

Use the browser form, or the tested HTTP path below. This creates a new video; it
does not replace the preloaded `DEMO_VIDEO_ID` used for repeatable hash auditing.

```sh
curl --fail-with-body -sS -F 'title=Live interview upload' \
  -F 'file=@data/interview-sample.mp4' http://127.0.0.1:8080/api/videos \
  -o data/interview-live-upload.json
jq '{id,status,title}' data/interview-live-upload.json
```

Open `/watch/ID` using the returned ID and press Play. For a tested decoder fallback,
use the full-clip command below; silent exit 0 means success, errors are nonzero:

```sh
ffmpeg -v error -xerror -i "http://127.0.0.1:8080/stream/$DEMO_VIDEO_ID/manifest.mpd" \
  -map 0:v:0 -map 0:v:1 -map '0:a?' -f null -
```

### Join (1:00)

```sh
docker compose --profile scale up -d --no-build --wait --wait-timeout 60 storage4
docker compose exec -T web admin add node4 storage4:9001
docker compose exec -T web admin migration
python3 scripts/inspect-distribution.py "$DEMO_VIDEO_ID" \
  --baseline data/interview-distribution-live-before.json \
  > data/interview-distribution-live-after-add.json
jq '{files_per_node,media_segments_per_node,baseline_content_verified}' \
  data/interview-distribution-live-after-add.json
```

For the rehearsed video, files become node1=2, node2=4, node3=5, node4=4. A new
live upload adds other files, so CLI migration totals can exceed four. Node join
triggers affected-file migration automatically after the explicit admin add;
it is not background autoscaling or automatic repair.

### Graceful removal (1:45)

```sh
docker compose exec -T web admin remove node2
# Stop only after state is committed. Do not continue on a migration error.
docker compose stop storage2
python3 scripts/inspect-distribution.py "$DEMO_VIDEO_ID" \
  --baseline data/interview-distribution-live-before.json \
  > data/interview-distribution-live-after-remove.json
jq '{files_per_node,baseline_content_verified}' \
  data/interview-distribution-live-after-remove.json
ffmpeg -v error -xerror -i "http://127.0.0.1:8080/stream/$DEMO_VIDEO_ID/manifest.mpd" \
  -map 0:v:0 -map 0:v:1 -map '0:a?' -f null -
docker compose exec -T web admin health
```

The rehearsal left 15 byte-identical files on node1/node3/node4 and decoded after
node2 stopped. This proves planned redistribution, not recovery from a crashed node.

### Raft failure demonstration (2:45)

Use the prepared `ec` function and refreshed leader/survivor variables:

```sh
ec exec -T "$INTERVIEW_ETCD_SURVIVOR" /usr/local/bin/etcdctl \
  --endpoints="$INTERVIEW_ETCD_ENDPOINTS" put /interview/demo-probe before-failure
ec kill --signal SIGKILL "$INTERVIEW_ETCD_LEADER"
ec exec -T "$INTERVIEW_ETCD_SURVIVOR" /usr/local/bin/etcdctl \
  --endpoints="$INTERVIEW_ETCD_ENDPOINTS" --command-timeout=10s get /interview/demo-probe
ec exec -T "$INTERVIEW_ETCD_SURVIVOR" /usr/local/bin/etcdctl \
  --endpoints="$INTERVIEW_ETCD_ENDPOINTS" --command-timeout=10s put /interview/demo-probe after-failure
curl --fail-with-body -sS "http://127.0.0.1:8081/api/videos/$INTERVIEW_ETCD_VIDEO_ID" \
  | jq '{id,status,title}'
# Restore the member with its existing volume:
ec up -d --no-deps --wait --wait-timeout 60 "$INTERVIEW_ETCD_LEADER"
ec exec -T "$INTERVIEW_ETCD_SURVIVOR" /usr/local/bin/etcdctl \
  --endpoints="$INTERVIEW_ETCD_ENDPOINTS" --command-timeout=10s endpoint health
```

Expected: probe `before-failure` remains readable; the new put returns `OK`;
application metadata stays ready. A short election interruption is possible.
The application records live under `/tritontube/state`; the separate probe key
is a native KV illustration, not a replacement for the application failover test.
All media nodes stay running. The native etcd cluster replicates its metadata log;
TritonTube does not implement Raft and does not replicate media.

The complete automated test was run separately:

```sh
/tmp/tritontube-go-env make test-etcd
# Or make test-etcd with Go installed normally on PATH.
```

It additionally verifies new application uploads, concurrent metadata CAS clients,
etcd-backed node changes and failure after a second member is killed. Two failures
remove a three-node cluster's majority. Do not kill a second member in the timed
live demo; the negative case is in the recorded automated test. No five-node or
two-failure survival claim is made.

## Restore the repeatable baseline after the demo

Run this only after the completed node4-add/node2-remove sequence above. Keep data.

```sh
docker compose up -d --no-build --wait --wait-timeout 60 storage2
docker compose exec -T web admin add node2 storage2:9001
docker compose exec -T web admin remove node4
docker compose --profile scale stop storage4
docker compose exec -T web admin list
python3 scripts/inspect-distribution.py "$DEMO_VIDEO_ID" \
  --baseline data/interview-distribution-live-before.json
```

If stopping everything, `make down` and `make etcd-down` preserve volumes. Never
use `down --volumes` as a casual demo reset. On a restart with node4 still committed,
start profile `scale` too. Do not change `-nodes` to perform a membership migration.

## Architecture explanation (30 seconds)

“One Go HTTP coordinator accepts uploads, manages metadata and owns an immutable
consistent-hash ring snapshot. Three independent Go processes expose streaming
gRPC over separate filesystem roots. FFmpeg produces VOD MPEG-DASH with 360p and
720p H.264 video and AAC audio when present. Each `videoID/filename` is independently
placed. The coordinator records size, SHA-256 and ownership, serves a verified MPD
and segments over HTTP, and embeds a small library UI with dash.js playback.
SQLite is the default metadata backend. Optional etcd uses its official client
and native replicated metadata transactions, while the application still has one
coordinator and unreplicated media.”

## Consistent hashing and migration explanation

- Both key and virtual-node positions use SHA-256's first eight bytes as big-endian
  uint64. The default is 100 virtual nodes per stable node ID. Node position input
  is `node:<UTF-8 byte length>:<nodeID>:<replica index>`, independent of the address.
- Sort by position, node ID and replica index. Binary search selects the first
  position greater than or equal to the key; wrap to the first point if needed.
  Collisions therefore have deterministic ownership independent of insertion order.
- A join moves only keys newly owned by the joining node. A fourth equal node owns
  roughly one quarter on average, not exactly 25% for every finite sample/ring.
- Migration compares recorded old ownership with the old ring, calculates the new
  ring, copies only affected files, verifies source and destination size/SHA-256,
  and independently reads the destination back. Only then does one metadata
  transaction publish membership/ownership. Source deletion is afterward.
- A global routing write lock pauses file operations during this simple migration;
  concurrent topology changes are rejected. Storage UUIDs reject DNS aliases/shared
  directories so cleanup cannot delete a destination through its source alias.
- Known failure keeps old routing and good source copies. A possibly committed
  remote transaction is reconciled by token; if unresolved, uploads/topology changes
  are fenced until restart rather than guessing and deleting data.

The Python audit is independent of Go routing: it enumerates actual files on each
committed Compose service, hashes HTTP responses, calculates ring owners itself,
and optionally compares all keys/checksums against a saved baseline.

## gRPC communication details

`proto/storage/v1/storage.proto` defines client-streaming Upload (header then 64 KiB
chunks), server-streaming Download and List, and unary Delete/Info. Info returns a
persistent storage-directory UUID; standard gRPC health reports SERVING. Upload
requires SHA-256 and a canonical relative key, bounds each file to 1 GiB, hashes
incrementally and syncs/atomically renames only a verified complete file. `os.Root`
confines paths, including symlink escapes. Clients propagate request deadlines and
cancellation and report short writes/server rejection status correctly. This is
plaintext trusted-local gRPC; mTLS/authentication are not implemented.

## Five challenging interview questions and answers

### 1. Why this ring instead of hash modulo N? What about collisions and imbalance?

Modulo changes many existing mappings when N changes. A stable ring keeps existing
positions and moves only the new node's intervals on join. Virtual nodes reduce
variance but do not guarantee exact balance or account for disk capacity/load.
SHA-256 truncation and big-endian interpretation are architecture-independent;
ties sort deterministically by node ID/index. The tests force collisions, boundary
wraparound and measure remapping over 10,000 keys. Production placement would need
capacity weights, monitoring and controlled rebalancing.

### 2. What happens if migration fails halfway, or immediately after commit?

Before publication, old routes and source copies remain authoritative; destination
duplicates can be left behind. All affected destinations must pass read-back
verification before the metadata transaction. After commit, new routing is durable
and old copies are only cleanup. A crash can leave duplicates; cleanup does not
resume automatically. This is not a distributed ACID transaction across machines,
not power-loss durability, and not replication. An unexpected failure of a sole
owner still loses availability of its files.

### 3. What if etcd committed, but the coordinator never received the reply?

An RPC error cannot establish rollback. A unique token and fresh linearizable read
can confirm the topology commit. If inconclusive, the coordinator retains sources,
marks the migration indeterminate in memory, blocks further file writes/topology
mutations and fails readiness. Restart after restoring quorum loads authoritative
membership. It must not overwrite a potentially committed durable status with a
guessed failure. The lost-response regression injects an error after a real commit
and verifies preserved data, fenced mutations and safe reconstruction.

### 4. Does native etcd Raft make the video service fault tolerant?

Only metadata is replicated here. A three-member etcd cluster needs two for quorum;
leader loss causes an election and surviving-quorum reads/writes can continue.
The test kills the identified leader, verifies old/new metadata and actual uploads,
then requires errors after losing two members. Media has one owner per file, the
web process is singular, and all local containers share a host. There is no
application-level Raft, media replication, coordinator lease or automatic media
repair. Multiple web coordinators are unsupported even though repository CAS
supports concurrent metadata clients.

### 5. Does streaming mean constant resource cost? How would you productionize it?

Memory for gRPC chunks is bounded, but HTTP multipart/transcoding/verified response
spooling consume disk and CPU. Range responses fetch a whole file before serving
the range; many concurrent viewers can exhaust spool space. Uploads are synchronous
and limited to two requests, 512 MiB and ten minutes of input; FFmpeg has an eight-
minute deadline. I would use durable background jobs, replicated object storage,
CDN delivery, signed URLs, tenant quotas, mTLS/auth, backpressure and metrics. For
metadata scale, replace the 1 MiB whole-state etcd snapshot with a per-record and
immutable-generation design that preserves atomic routing publication, then add
coordinator lease/fencing and test real failure domains.

## Known limitations and realistic improvements

| Current limit | Practical next step |
| --- | --- |
| Unreplicated media; only graceful changes are safe | Replicated object storage or tested replication/repair protocol |
| One coordinator; SQLite is single host | Lease/fencing, versioned routing and tested coordinator failover |
| Etcd state rewrites all metadata, capped at 1 MiB | Per-record metadata plus atomic inventory-generation pointer |
| All local etcd members share one Docker host | Distinct hosts/zones, proper backups, TLS/auth and recovery exercises |
| Plain HTTP/gRPC, no auth/quotas | Authentication, authorization, mTLS, signed playback URLs and tenant isolation |
| Synchronous encoding and limited representations | Durable job queue, bounded workers, retries/idempotency, quality ladder |
| Disk spooling and no global viewer bound | Object-store/CDN delivery, resource limits, metrics and request backpressure |
| Failed/abrupt operations can leave orphan/duplicate files | Persistent migration journal, reconciliation and safe garbage collection |
| Health means availability, not full integrity/capacity | Integrity scrubbing, disk/quota monitoring and alerts |
| External dash.js CDN; visual playback not agent-observed | Personally rehearse browser; consider licensed pinned local asset for offline demos |
| Local CI commands pass; GitHub Actions not run remotely | Review/commit and run remote CI when authorized; no push was made here |

The Go wrapper is under `/tmp` and may not survive a reboot/cleanup. Install Go
1.26+ normally before the interview if it is absent. Warm Docker application and
manual Raft demos do not require host Go. No changes to host capture permissions
were made. Everything in Git is currently untracked with no local commit; inspect
the final verification report before staging/committing. Do not push until reviewed.
