# Optional etcd metadata backend

TritonTube can store metadata in a real three-member etcd cluster using the official
`go.etcd.io/etcd/client/v3` client (pinned to v3.6.16). SQLite remains the default.
The etcd implementation has independent persistence logic behind the common
repository interface. It does not use SQL queries, fall back to SQLite, or implement
Raft. Its reads use etcd's default linearizable consistency; writes use revision
comparisons and native transactions. See [etcd's API guarantees](https://etcd.io/docs/v3.6/learning/api_guarantees/).

**This replicates metadata, not media files.** Storage remains unreplicated. An
unexpected media-node failure can still make its files unavailable. Graceful node
redistribution is a separate protocol requiring readable sources and verified copies.

## Start the local three-member deployment

Prerequisites: Docker Engine/Desktop and Compose v2+. Host Go and FFmpeg/ffprobe
are needed to run the automated failover test; the application containers include
their own FFmpeg. The deployment pins the official etcd v3.6.16 container image and
uses static native clustering with three separate data volumes, following the
[official container configuration](https://etcd.io/docs/v3.6/op-guide/container/).

Use a different HTTP port if the default SQLite demo already runs on 8080:

```sh
TRITONTUBE_HTTP_PORT=8081 make etcd-up
# Open http://127.0.0.1:8081 and upload an MP4.
docker compose -p tritontube-etcd -f docker-compose.yml \
  -f docker-compose.etcd.yml --profile etcd exec web admin list
docker compose -p tritontube-etcd -f docker-compose.yml \
  -f docker-compose.etcd.yml --profile etcd exec etcd1 \
  /usr/local/bin/etcdctl \
  --endpoints=http://etcd1:2379,http://etcd2:2379,http://etcd3:2379 \
  --write-out=table endpoint status
# Stop this separate deployment while preserving all volumes:
make etcd-down
```

The separate project `tritontube-etcd` keeps the SQLite demo's volumes and media
untouched. There is **no automatic metadata import or backend migration**. An
empty etcd namespace starts with an empty library; existing SQLite media are not
automatically discovered. Keep each metadata backend with its own storage data.

The overlay enables profile `etcd`, starts one web process and three filesystem
storage processes, and configures web with all three etcd client endpoints. etcd
peer ports are internal to the Compose network. Client ports bind to host loopback
at 2379, 2381 and 2383; override `TRITONTUBE_ETCD1_PORT`,
`TRITONTUBE_ETCD2_PORT` and `TRITONTUBE_ETCD3_PORT` if those are occupied.
`TRITONTUBE_HTTP_PORT` sets the web port. These are unauthenticated plaintext local
demo endpoints. TLS and authentication configuration are not exposed by this
backend's current CLI; production secure connectivity requires further work.

etcd uses `restart: "no"` so a deliberately failed member stays stopped for the
failover demonstration. Restart a member explicitly to recover it using its
persisted volume. App/storage restart policies retain their normal demo behavior.
Do not delete member data directories or rebootstrap a new cluster over an existing
cluster's data. `make etcd-down` does not delete volumes.

## Use an externally configured etcd cluster

With the three local filesystem storage nodes already running:

```sh
./bin/web -listen 127.0.0.1:8080 -work data/etcd-tmp \
  -metadata etcd \
  -etcd-endpoints http://127.0.0.1:2379,http://127.0.0.1:2381,http://127.0.0.1:2383 \
  -etcd-prefix /tritontube
```

Use a namespace and storage set dedicated to this deployment. The prefix defaults
to `/tritontube`; the sole state key is `<prefix>/state`. Startup requires a reachable
quorum and initializes only an absent state; persisted membership wins over seed
flags. The `-db` path is used only in SQLite mode. Missing etcd quorum is an explicit
error, never a reason to fall back to a different backend.

## Metadata model and consistency

For this bounded demo, the state is one schema-versioned JSON snapshot containing
video records, complete recorded file inventory, node membership, virtual-node
count and migration status. Each write reads a linearizable snapshot and uses a
native `ModRevision` compare-and-swap transaction. Concurrent updates retry from a
new snapshot, preserving unrelated changes. The backend bounds the whole operation
to five seconds and 64 comparison retries. These are contention/error bounds, not
an unconditional availability promise during every election.

A topology commit changes membership, all file owners and committed migration
status in one transaction. Size/checksum and proposed ring ownership are checked
before publication. This single-key model avoids partially publishing an inventory
or running past etcd's transaction operation count, but rewrites all metadata for
each mutation. **The encoded snapshot is capped at 1,048,576 bytes.** Writes exceeding
that cap fail before committing. This is a small-library demonstration, not a
production-scale metadata schema. Exhausting the limit after a file was stored can
leave an orphan, as with other failures between media publication and metadata
recording; no automatic garbage collector exists.

An RPC error does not prove a remote transaction failed. Topology commits carry a
unique token. After an error, the backend attempts a fresh linearizable read: if
that token is present, the commit is confirmed. Otherwise it returns an indeterminate
outcome. The coordinator retains all source copies, marks its in-memory migration
`indeterminate`, rejects further file writes/topology changes and returns degraded
health. It does not overwrite a potentially committed durable record with a guessed
failure. Restore metadata quorum, then restart the single coordinator to load the
authoritative membership. Old duplicate copies may remain; cleanup is manual.

This still supports **one web/coordinator process**. Native etcd replication and
CAS do not provide a coordinator lease, reconcile separate in-memory rings, or
make multiple web processes safe. Concurrent repository clients are tested for
metadata updates, but multiple application coordinators remain unsupported.

## Reproduce the actual failure test

First run the core gates, then the optional test:

```sh
make check
make test-compose
make test-etcd
```

For this workspace's temporary Go toolchain, prefix Go/Make commands with
`/tmp/tritontube-go-env`. `make test-etcd` requires Docker and FFmpeg/ffprobe, builds
an isolated application image and runs a unique three-member etcd project with
separate volumes and dynamic loopback ports. It uses race detection for the host
repository/client test code. The official etcd servers use their native released
Raft implementation.

The test:

1. Tests metadata create/read/list/status/inventory, duplicate/missing records,
   startup recovery, failed transaction rollback, atomic topology publication,
   a 1 MiB write rejection and concurrent writes through separate clients.
2. Uploads a genuine generated H.264/AAC MP4 through the etcd-backed web process.
3. Identifies the actual etcd leader and kills it with SIGKILL, leaving two of three
   members. It checks existing video, inventory and membership continuity, new
   metadata writes/readback through a survivor-only client, new uploads and HTTP
   DASH decoding of both video representations and audio.
4. Exercises a real storage join/graceful removal and metadata topology transactions
   while one etcd member is down. This is planned redistribution, not media recovery.
5. Kills a second etcd member and requires linearizable reads and writes to fail
   without quorum. It never serves cached data as a successful quorum read.
6. Removes only its own containers, volumes and application image on normal exit;
   the shared official etcd image is retained. Hard termination can bypass cleanup.

A three-member cluster can continue with two members; losing two removes its
majority. The supplied local deployment/test is three-member only. **No five-member
or two-failure survival claim is made.** These containers all share one Docker host,
so the test is a process-failure demonstration, not host/zone-failure validation.
Media nodes are not crashed in this test; stopping the optional media node occurs
only after its successful graceful removal. Media storage has no replication or
unplanned storage-file fault tolerance.

See [Phase 7 results](PHASE7_RESULTS.md) for executed commands and captured output.
