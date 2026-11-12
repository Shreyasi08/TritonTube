#!/bin/sh
set -eu
# Fail explicitly here rather than accidentally reporting skipped media tests.
go_cmd=${GO:-go}
for tool in "$go_cmd" ffmpeg ffprobe; do
  command -v "$tool" >/dev/null 2>&1 || { echo "Required end-to-end test tool missing: $tool" >&2; exit 1; }
done
cd "$(dirname "$0")/.."
exec "$go_cmd" test -count=1 -v ./tests -run '^(TestDASHEndToEnd|TestAdminNodeChangesKeepDASHPlayable)$' -timeout 2m
