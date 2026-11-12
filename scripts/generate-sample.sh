#!/bin/sh
set -eu
command -v ffmpeg >/dev/null 2>&1 || { echo 'FFmpeg is required. Install it and run this script again.' >&2; exit 1; }
output=${1:-data/sample.mp4}
if [ -e "$output" ]; then echo "Refusing to overwrite $output" >&2; exit 1; fi
mkdir -p "$(dirname "$output")"
ffmpeg -hide_banner -loglevel error -nostdin -n \
  -f lavfi -i 'testsrc2=size=640x360:rate=30' \
  -f lavfi -i 'sine=frequency=440:sample_rate=48000' \
  -t 6 -c:v libx264 -preset veryfast -pix_fmt yuv420p -threads 2 \
  -c:a aac -shortest -movflags +faststart "$output"
echo "Created $output"
