#!/usr/bin/env python3
"""Read-only Compose audit: physical SHA-256 + HTTP bytes + independent ring lookup."""
import argparse
import bisect
import collections
import hashlib
import json
import pathlib
import re
import subprocess
import urllib.parse
import urllib.request


def position(value):
    return int.from_bytes(hashlib.sha256(value.encode("utf-8")).digest()[:8], "big")


def ring(identities, replicas):
    points = sorted(
        (position(f"node:{len(identity.encode('utf-8'))}:{identity}:{replica}"), identity, replica)
        for identity in identities
        for replica in range(replicas)
    )
    positions = [point[0] for point in points]

    def owner(key):
        index = bisect.bisect_left(positions, position(key))
        return points[index % len(points)][1]

    return owner


def audit(args):
    if not re.fullmatch(r"[0-9a-f]{32}", args.video_id):
        raise ValueError("video ID must be the 32-character lowercase hex ID returned by upload")
    if not 1 <= args.virtual_nodes <= 10000:
        raise ValueError("virtual-node count must be between 1 and 10000")
    base = args.url.rstrip("/")
    with urllib.request.urlopen(base + "/api/cluster/nodes", timeout=10) as response:
        nodes = json.load(response)
    if not nodes:
        raise ValueError("empty membership")
    identities = [node["id"] for node in nodes]
    expected_owner = ring(identities, args.virtual_nodes)
    files = {}
    counts = collections.Counter({identity: 0 for identity in identities})
    segments = collections.Counter({identity: 0 for identity in identities})
    root = "/data/files/" + args.video_id
    # Arguments are passed separately; no application-supplied data is interpolated into shell code.
    command = 'if [ -d "$1" ]; then find "$1" -type f -exec sha256sum {} +; fi'
    for node in nodes:
        service = urllib.parse.urlsplit("//" + node["address"]).hostname
        if not service or not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_-]*", service):
            raise ValueError("audit expects Compose service hostnames in node addresses")
        result = subprocess.run(
            ["docker", "compose", "--project-name", args.project, "exec", "-T", service,
             "sh", "-c", command, "sh", root],
            check=True, capture_output=True, text=True, timeout=30,
        )
        for line in result.stdout.splitlines():
            checksum, filename = line.split(maxsplit=1)
            if not filename.startswith(root + "/"):
                raise ValueError("unexpected physical path: " + filename)
            key = filename.removeprefix("/data/files/")
            if key in files:
                raise ValueError("duplicate physical copy on committed nodes: " + key)
            if expected_owner(key) != node["id"]:
                raise ValueError("physical owner disagrees with SHA-256 ring: " + key)
            with urllib.request.urlopen(base + "/stream/" + urllib.parse.quote(key, safe="/"), timeout=30) as response:
                digest = hashlib.sha256()
                for chunk in iter(lambda: response.read(65536), b""):
                    digest.update(chunk)
            if digest.hexdigest() != checksum:
                raise ValueError("HTTP bytes disagree with physical storage checksum: " + key)
            files[key] = {"owner": node["id"], "sha256": checksum}
            counts[node["id"]] += 1
            name = pathlib.PurePosixPath(key).name
            if name.startswith("chunk-") and name.endswith(".m4s"):
                segments[node["id"]] += 1
    if not files or args.video_id + "/manifest.mpd" not in files:
        raise ValueError("video has no retrievable physical DASH manifest")
    if args.baseline:
        before = json.loads(args.baseline.read_text())["files"]
        if before.keys() != files.keys():
            raise ValueError("file set changed: missing or unexpected file after migration")
        for key, item in before.items():
            if files[key]["sha256"] != item["sha256"]:
                raise ValueError("content changed after migration: " + key)
    report = {
        "video_id": args.video_id, "virtual_nodes": args.virtual_nodes,
        "file_count": len(files), "files_per_node": dict(counts),
        "media_segments_per_node": dict(segments), "ring_and_http_checksums_verified": True,
        "baseline_content_verified": bool(args.baseline), "files": files,
    }
    if args.plan_add:
        if args.plan_add in identities:
            raise ValueError("proposed node identity already exists")
        new_owner = ring(identities + [args.plan_add], args.virtual_nodes)
        affected = [key for key in files if new_owner(key) != files[key]["owner"]]
        report["planned_add"] = {"id": args.plan_add, "affected_count": len(affected), "files": affected}
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("video_id")
    parser.add_argument("--url", default="http://127.0.0.1:8080")
    parser.add_argument("--project", default="tritontube")
    parser.add_argument("--virtual-nodes", type=int, default=100)
    parser.add_argument("--baseline", type=pathlib.Path)
    parser.add_argument("--plan-add")
    args = parser.parse_args()
    try:
        print(json.dumps(audit(args), indent=2, sort_keys=True))
    except (ValueError, OSError, subprocess.SubprocessError) as error:
        parser.exit(1, f"Distribution verification failed: {error}\n")


if __name__ == "__main__":
    main()
