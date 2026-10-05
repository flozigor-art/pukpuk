package node

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"langplant-crm/internal/proto"
)

// The node does all CPU-heavy work (the VPS is small): posters, thumbnails,
// preview proxies, technical metadata and audio waveforms.

func (n *Node) deriveWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-n.deriveQ:
			if err := n.derive(ctx, m); err != nil {
				slog.Warn("processing failed", "sha", m.Sha[:12], "err", err)
				n.send(proto.Msg{T: proto.TDeriveFailed, Sha: m.Sha, Error: err.Error()})
			}
			n.deriveN.Add(-1)
		}
	}
}

func (n *Node) derive(ctx context.Context, m proto.Msg) error {
	src := n.blobPath(m.Sha)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("file not found on the storage PC")
	}
	jctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	probe, err := n.ffprobe(jctx, src)
	if err != nil && !strings.HasPrefix(m.Mime, "image/") {
		return fmt.Errorf("ffprobe: %w", err)
	}
	if probe == nil {
		probe = &proto.Probe{Meta: map[string]any{}}
	}
	for _, name := range m.Want {
		if !proto.ValidDerived(name) {
			continue
		}
		out := n.derivedPath(m.Sha, name)
		if _, err := os.Stat(out); err != nil {
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := n.generate(jctx, m.Sha, src, name, m.Mime, probe, out); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		if err := n.pushDerived(jctx, m.Sha, name, out); err != nil {
			return fmt.Errorf("upload %s: %w", name, err)
		}
	}
	if strings.HasPrefix(m.Mime, "audio/") {
		if peaks, err := n.peaks(jctx, src, 200); err == nil {
			probe.Peaks = peaks
		} else {
			slog.Warn("waveform", "sha", m.Sha[:12], "err", err)
		}
	}
	n.send(proto.Msg{T: proto.TProbe, Sha: m.Sha, Probe: probe})
	return nil
}

func (n *Node) ffprobe(ctx context.Context, src string) (*proto.Probe, error) {
	cmd := exec.CommandContext(ctx, n.cfg.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", src)
	out, err := cmd.Output()
	if err != nil {
		return nil, cmdErr(err)
	}
	var raw struct {
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
			BitRate    string `json:"bit_rate"`
		} `json:"format"`
		Streams []struct {
			CodecType    string            `json:"codec_type"`
			CodecName    string            `json:"codec_name"`
			Width        int               `json:"width"`
			Height       int               `json:"height"`
			RFrameRate   string            `json:"r_frame_rate"`
			SampleRate   string            `json:"sample_rate"`
			Channels     int               `json:"channels"`
			Tags         map[string]string `json:"tags"`
			SideDataList []struct {
				Rotation float64 `json:"rotation"`
			} `json:"side_data_list"`
			Disposition map[string]int `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	p := &proto.Probe{Meta: map[string]any{"format": raw.Format.FormatName}}
	if d, err := strconv.ParseFloat(raw.Format.Duration, 64); err == nil {
		p.DurationMs = int64(d * 1000)
	}
	if br, err := strconv.ParseInt(raw.Format.BitRate, 10, 64); err == nil {
		p.Meta["bitrate"] = br
	}
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue // cover art inside an mp3
			}
			if p.Width == 0 {
				p.Width, p.Height = s.Width, s.Height
				rot := 0.0
				if r, err := strconv.ParseFloat(s.Tags["rotate"], 64); err == nil {
					rot = r
				}
				for _, sd := range s.SideDataList {
					if sd.Rotation != 0 {
						rot = sd.Rotation
					}
				}
				if int(rot)%180 != 0 {
					p.Width, p.Height = p.Height, p.Width
				}
				p.Meta["video_codec"] = s.CodecName
				if a, b, ok := strings.Cut(s.RFrameRate, "/"); ok {
					x, _ := strconv.ParseFloat(a, 64)
					y, _ := strconv.ParseFloat(b, 64)
					if y > 0 {
						p.Meta["fps"] = float64(int(x/y*100)) / 100
					}
				}
			}
		case "audio":
			if _, set := p.Meta["audio_codec"]; !set {
				p.Meta["audio_codec"] = s.CodecName
				p.Meta["sample_rate"] = s.SampleRate
				p.Meta["channels"] = s.Channels
			}
		}
	}
	return p, nil
}

func (n *Node) generate(ctx context.Context, sha, src, name, mime string, probe *proto.Probe, out string) error {
	tmp := out + ".tmp" + filepath.Ext(out)
	defer os.Remove(tmp)
	var args []string
	switch {
	case name == proto.DerivedPoster && strings.HasPrefix(mime, "video/"):
		at := 1.0
		if probe.DurationMs > 0 {
			at = min(1.0, float64(probe.DurationMs)/1000*0.25)
		}
		args = []string{"-ss", fmt.Sprintf("%.2f", at), "-i", src, "-frames:v", "1",
			"-vf", "scale='min(1280,iw)':'min(1280,ih)':force_original_aspect_ratio=decrease", "-q:v", "3", "-f", "image2", tmp}
	case name == proto.DerivedThumb && strings.HasPrefix(mime, "video/"):
		poster := n.derivedPath(sha, proto.DerivedPoster)
		in := src
		pre := []string{"-ss", "1"}
		if _, err := os.Stat(poster); err == nil {
			in, pre = poster, nil
		}
		args = append(pre, "-i", in, "-frames:v", "1",
			"-vf", "scale='min(480,iw)':'min(480,ih)':force_original_aspect_ratio=decrease", "-q:v", "4", "-f", "image2", tmp)
	case name == proto.DerivedThumb && strings.HasPrefix(mime, "image/"):
		args = []string{"-i", src, "-frames:v", "1",
			"-vf", "scale='min(480,iw)':'min(480,ih)':force_original_aspect_ratio=decrease", "-q:v", "4", "-f", "image2", tmp}
	case name == proto.DerivedPreview && strings.HasPrefix(mime, "video/"):
		h := n.cfg.PreviewHeight
		args = []string{"-i", src, "-map", "0:v:0", "-map", "0:a:0?",
			"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", h, h),
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "28", "-profile:v", "main", "-pix_fmt", "yuv420p",
			"-c:a", "aac", "-b:a", "96k", "-ac", "2", "-movflags", "+faststart", "-f", "mp4", tmp}
	default:
		return fmt.Errorf("cannot make %s from %s", name, mime)
	}
	if err := n.ffmpeg(ctx, args...); err != nil {
		// a very short clip: retry the poster from the first frame
		if name == proto.DerivedPoster {
			args[1] = "0"
			if err2 := n.ffmpeg(ctx, args...); err2 != nil {
				return err
			}
		} else {
			return err
		}
	}
	return os.Rename(tmp, out)
}

func (n *Node) ffmpeg(ctx context.Context, args ...string) error {
	full := append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)
	cmd := exec.CommandContext(ctx, n.cfg.FFmpeg, full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("%v: %s", err, msg)
	}
	return nil
}

// peaks decodes audio to mono 16-bit PCM and returns bins normalised to 0..100.
func (n *Node) peaks(ctx context.Context, src string, bins int) ([]int, error) {
	cmd := exec.CommandContext(ctx, n.cfg.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-i", src,
		"-vn", "-ac", "1", "-ar", "4000", "-f", "s16le", "-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, rerr := io.ReadAll(bufio.NewReaderSize(stdout, 1<<16))
	if rerr != nil {
		cmd.Wait()
		return nil, rerr
	}
	samples := make([]int16, len(data)/2)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
	}
	if err := cmd.Wait(); err != nil {
		return nil, cmdErr(err)
	}
	if len(samples) == 0 {
		return nil, errors.New("no audio")
	}
	return binPeaks(samples, bins), nil
}

func binPeaks(samples []int16, bins int) []int {
	if bins > len(samples) {
		bins = len(samples)
	}
	raw := make([]float64, bins)
	peak := 0.0
	for i := range raw {
		from, to := i*len(samples)/bins, (i+1)*len(samples)/bins
		m := 0.0
		for _, s := range samples[from:to] {
			a := float64(s)
			if a < 0 {
				a = -a
			}
			if a > m {
				m = a
			}
		}
		raw[i] = m
		peak = max(peak, m)
	}
	out := make([]int, bins)
	for i, v := range raw {
		if peak > 0 {
			out[i] = int(v / peak * 100)
		}
	}
	return out
}

func (n *Node) pushDerived(ctx context.Context, sha, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	ws, err := n.dial(ctx, "/api/node/derived/"+sha+"/"+name)
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	mime := "image/jpeg"
	if name == proto.DerivedPreview {
		mime = "video/mp4"
	}
	if err := streamFile(ctx, ws, proto.StreamHeader{Size: st.Size(), Len: st.Size(), Mime: mime}, f); err != nil {
		return err
	}
	// wait for the server to confirm by closing the socket normally
	rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, _, err = ws.Read(rctx)
	if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
		return nil
	}
	return fmt.Errorf("server did not accept the file: %v", err)
}

func cmdErr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
