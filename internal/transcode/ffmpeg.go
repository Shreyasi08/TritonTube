// Package transcode generates MPEG-DASH media using an external FFmpeg process.
package transcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
)

type Uploader interface {
	Upload(context.Context, string, string, io.Reader) (cluster.File, error)
}
type FFmpeg struct {
	Router      Uploader
	Binary      string
	ProbeBinary string
}
type cappedBuffer struct{ data []byte }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	const limit = 16 * 1024
	if len(p) >= limit {
		b.data = append(b.data[:0], p[len(p)-limit:]...)
	} else {
		b.data = append(b.data, p...)
		if len(b.data) > limit {
			b.data = b.data[len(b.data)-limit:]
		}
	}
	return n, nil
}
func (f *FFmpeg) Process(ctx context.Context, id, input string) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	binary := f.Binary
	if binary == "" {
		binary = "ffmpeg"
	}
	probe := f.ProbeBinary
	if probe == "" {
		probe = "ffprobe"
	}
	// Probe before allocating output. Bound duration to make demo resource limits explicit.
	var probeOut, probeErr cappedBuffer
	cmd := exec.CommandContext(ctx, probe, "-v", "error", "-protocol_whitelist", "file,pipe", "-show_entries", "format=duration:stream=codec_type", "-of", "json", input)
	cmd.Stdout = &probeOut
	cmd.Stderr = &probeErr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffprobe: %w: %s", errors.Join(err, ctx.Err()), probeErr.data)
	}
	var info struct {
		Streams []struct {
			Type string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(probeOut.data, &info); err != nil {
		return fmt.Errorf("probe response: %w", err)
	}
	duration, err := strconv.ParseFloat(info.Format.Duration, 64)
	if err != nil || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > 600 {
		return fmt.Errorf("video duration must be between 0 and 600 seconds")
	}
	hasVideo, hasAudio := false, false
	for _, s := range info.Streams {
		hasVideo = hasVideo || s.Type == "video"
		hasAudio = hasAudio || s.Type == "audio"
	}
	if !hasVideo {
		return fmt.Errorf("MP4 has no video stream")
	}
	dir, err := os.MkdirTemp(filepath.Dir(input), "dash-")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary DASH output: %w", err))
		}
	}()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-protocol_whitelist", "file,pipe", "-i", input, "-filter_complex_threads", "2", "-filter_complex", "[0:v:0]split=2[v0][v1];[v0]scale=-2:360[low];[v1]scale=-2:720[high]", "-map", "[low]", "-map", "[high]"}
	if hasAudio {
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", "128k")
	}
	adaptations := "id=0,streams=v"
	if hasAudio {
		adaptations += " id=1,streams=a"
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-threads", "2", "-r", "30", "-g", "60", "-keyint_min", "60", "-sc_threshold", "0", "-b:v:0", "600k", "-b:v:1", "1800k", "-seg_duration", "2", "-use_template", "1", "-use_timeline", "1", "-adaptation_sets", adaptations, "-f", "dash", filepath.Join(dir, "manifest.mpd"))
	var stderr cappedBuffer
	cmd = exec.CommandContext(ctx, binary, args...)
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", errors.Join(err, ctx.Err()), strings.TrimSpace(string(stderr.data)))
	}
	manifestInfo, err := os.Stat(filepath.Join(dir, "manifest.mpd"))
	if err != nil {
		return fmt.Errorf("missing DASH manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() == 0 {
		return fmt.Errorf("empty or invalid DASH manifest")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) < 3 {
		return fmt.Errorf("ffmpeg produced incomplete DASH output")
	}
	// Publish the manifest last. HTTP only exposes videos marked ready after all files succeed.
	for pass := 0; pass < 2; pass++ {
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() || info.Size() == 0 {
				return fmt.Errorf("invalid DASH output file %s", entry.Name())
			}
			manifest := entry.Name() == "manifest.mpd"
			if (pass == 1) != manifest {
				continue
			}
			filename := filepath.Join(dir, entry.Name())
			file, e := os.Open(filename)
			if e != nil {
				return e
			}
			h := sha256.New()
			_, e = io.Copy(h, file)
			if e == nil {
				_, e = file.Seek(0, io.SeekStart)
			}
			if e == nil {
				_, e = f.Router.Upload(ctx, id+"/"+entry.Name(), hex.EncodeToString(h.Sum(nil)), file)
			}
			file.Close()
			if e != nil {
				return fmt.Errorf("publish %s: %w", entry.Name(), e)
			}
		}
	}
	return nil
}
