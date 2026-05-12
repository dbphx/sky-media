package transcode

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yutopp/go-flv"
	flvtag "github.com/yutopp/go-flv/tag"
)

type Config struct {
	FFmpegBin        string
	StoragePath      string
	SegmentTime      int
	SegmentCount     int
	MasterName       string
	VideoCodec       string
	VideoPreset      string
	VideoTune        string
	VideoFPS         int
	AudioCodec       string
	AudioSampleRate  int
	RTSPTransport    string
	RTSPStimeoutUSec int
	MaxStreams       int
	IdleTimeoutSec   int
	Variants         []Variant
}

type Variant struct {
	Name         string
	Width        int
	Height       int
	VideoBitrate string
	MaxRate      string
	BufSize      string
	AudioBitrate string
}

type Manager struct {
	cfg     Config
	mu      sync.Mutex
	streams map[string]*Stream
}

type Stream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	enc    *flv.Encoder
	cancel context.CancelFunc
	timer  *time.Timer
}

func NewManager(cfg Config) *Manager {
	return &Manager{cfg: cfg, streams: make(map[string]*Stream)}
}

func (m *Manager) StartRTMP(app string, streamKey string) (*Stream, error) {
	key := streamPath(app, streamKey)

	m.mu.Lock()
	if s, ok := m.streams[key]; ok {
		if s.timer != nil {
			delete(m.streams, key)
			m.mu.Unlock()
			stopStream(s)
			m.mu.Lock()
		} else {
			m.mu.Unlock()
			return s, nil
		}
	}
	if len(m.streams) >= m.cfg.MaxStreams {
		m.mu.Unlock()
		return nil, fmt.Errorf("max streams reached")
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := buildArgs(m.cfg, "pipe:0", key)
	cmd := exec.CommandContext(ctx, m.cfg.FFmpegBin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		m.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("create ffmpeg stdin: %w", err)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		m.mu.Unlock()
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	enc, err := flv.NewEncoder(stdin, flv.FlagsAudio|flv.FlagsVideo)
	if err != nil {
		m.mu.Unlock()
		cancel()
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("create flv encoder: %w", err)
	}

	s := &Stream{cmd: cmd, stdin: stdin, enc: enc, cancel: cancel}
	m.streams[key] = s
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("ffmpeg exited for %s: %v", key, err)
		}
	}()
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) StartPull(app string, streamKey string, inputURL string) (*Stream, error) {
	key := streamPath(app, streamKey)

	m.mu.Lock()
	if s, ok := m.streams[key]; ok {
		delete(m.streams, key)
		m.mu.Unlock()
		stopStream(s)
		m.mu.Lock()
	}
	if len(m.streams) >= m.cfg.MaxStreams {
		m.mu.Unlock()
		return nil, fmt.Errorf("max streams reached")
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := buildArgs(m.cfg, inputURL, key)
	cmd := exec.CommandContext(ctx, m.cfg.FFmpegBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Unlock()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	s := &Stream{cmd: cmd, stdin: nil, enc: nil, cancel: cancel}
	m.streams[key] = s
	go func(snap *Stream, c *exec.Cmd) {
		if err := c.Wait(); err != nil {
			log.Printf("ffmpeg exited for %s: %v", key, err)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		cur, ok := m.streams[key]
		if !ok || cur != snap {
			return
		}
		if cur.timer != nil {
			cur.timer.Stop()
		}
		delete(m.streams, key)
	}(s, cmd)
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) ScheduleStop(app string, streamKey string) {
	key := streamPath(app, streamKey)
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.streams[key]
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(time.Duration(m.cfg.IdleTimeoutSec)*time.Second, func() { m.Stop(app, streamKey) })
}

func (m *Manager) Stop(app string, streamKey string) {
	key := streamPath(app, streamKey)
	m.mu.Lock()
	s, ok := m.streams[key]
	if ok {
		delete(m.streams, key)
	}
	m.mu.Unlock()
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	stopStream(s)
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	keys := make([]string, 0, len(m.streams))
	for k := range m.streams {
		keys = append(keys, k)
	}
	m.mu.Unlock()
	for _, k := range keys {
		m.StopPath(k)
	}
}

func (m *Manager) StopPath(path string) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return
	}
	m.Stop(parts[0], parts[1])
}

func (s *Stream) EncodeTag(tag *flvtag.FlvTag) error {
	if s == nil || s.enc == nil {
		return nil
	}
	return s.enc.Encode(tag)
}

func stopStream(s *Stream) {
	s.cancel()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func streamPath(app string, streamKey string) string {
	return filepath.ToSlash(filepath.Join(app, streamKey))
}

func buildArgs(cfg Config, inputURL string, streamPath string) []string {
	forceKeyFrames := fmt.Sprintf("expr:gte(t,n_forced*%d)", cfg.SegmentTime)
	gop := strconv.Itoa(cfg.SegmentTime * cfg.VideoFPS)
	audioSampleRate := strconv.Itoa(cfg.AudioSampleRate)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts+igndts+discardcorrupt",
		"-flags", "+low_delay",
	}
	args = append(args, rtspInputFlags(inputURL, cfg.RTSPTransport, cfg.RTSPStimeoutUSec)...)
	args = append(args, "-i", inputURL)
	for i, v := range cfg.Variants {
		maxRate, bufSize := normalizeRateControl(v.VideoBitrate, v.MaxRate, v.BufSize)
		args = append(args,
			"-map", "0:v:0?", "-map", "0:a:0?",
			"-c:v:"+strconv.Itoa(i), cfg.VideoCodec,
			"-preset", cfg.VideoPreset,
			"-tune", cfg.VideoTune,
			"-b:v:"+strconv.Itoa(i), v.VideoBitrate,
			"-maxrate:v:"+strconv.Itoa(i), maxRate,
			"-bufsize:v:"+strconv.Itoa(i), bufSize,
			"-s:v:"+strconv.Itoa(i), fmt.Sprintf("%dx%d", v.Width, v.Height),
			"-g", gop,
			"-keyint_min", gop,
			"-sc_threshold", "0",
			"-force_key_frames", forceKeyFrames,
			"-c:a:"+strconv.Itoa(i), cfg.AudioCodec,
			"-b:a:"+strconv.Itoa(i), v.AudioBitrate,
			"-ar", audioSampleRate,
		)
	}

	var streamMap []string
	for i := range cfg.Variants {
		streamMap = append(streamMap, fmt.Sprintf("v:%d,a:%d,name:%s", i, i, cfg.Variants[i].Name))
	}

	streamRoot := filepath.Join(cfg.StoragePath, streamPath)
	segmentPattern := filepath.Join(streamRoot, "%v", "segment_%06d.ts")
	variantPlaylistPattern := filepath.Join(streamRoot, "%v", "index.m3u8")
	_ = os.MkdirAll(streamRoot, 0o755)

	args = append(args,
		"-fps_mode", "passthrough",
		"-af", "aresample=async=1:first_pts=0",
		"-avoid_negative_ts", "make_zero",
		"-max_interleave_delta", "0",
		"-f", "hls",
		"-hls_time", strconv.Itoa(cfg.SegmentTime),
		"-hls_list_size", strconv.Itoa(cfg.SegmentCount),
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
		"-hls_delete_threshold", "1",
		"-master_pl_name", cfg.MasterName,
		"-hls_segment_filename", segmentPattern,
		"-var_stream_map", strings.Join(streamMap, " "),
		variantPlaylistPattern,
	)

	return args
}

func rtspInputFlags(inputURL string, transport string, stimeoutUSec int) []string {
	low := strings.ToLower(strings.TrimSpace(inputURL))
	if !strings.HasPrefix(low, "rtsp://") && !strings.HasPrefix(low, "rtsps://") {
		return nil
	}
	var out []string
	if t := strings.TrimSpace(transport); t != "" {
		out = append(out, "-rtsp_transport", t)
	}
	if stimeoutUSec > 0 {
		out = append(out, "-stimeout", strconv.Itoa(stimeoutUSec))
	}
	return out
}

func normalizeRateControl(videoBitrate string, maxRate string, bufSize string) (string, string) {
	vb, okVB := parseBitrateKbps(videoBitrate)
	mr, okMR := parseBitrateKbps(maxRate)
	bs, okBS := parseBitrateKbps(bufSize)
	if !okVB {
		return maxRate, bufSize
	}

	minMR := int(math.Ceil(float64(vb) * 1.2))
	if !okMR || mr < minMR {
		mr = minMR
	}

	minBS := int(math.Ceil(float64(mr) * 2.5))
	if !okBS || bs < minBS {
		bs = minBS
	}

	return fmt.Sprintf("%dk", mr), fmt.Sprintf("%dk", bs)
}

func parseBitrateKbps(v string) (int, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	if s == "" {
		return 0, false
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "k"):
		s = strings.TrimSuffix(s, "k")
		multiplier = 1.0
	case strings.HasSuffix(s, "m"):
		s = strings.TrimSuffix(s, "m")
		multiplier = 1000.0
	default:
		return 0, false
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 0, false
	}

	return int(math.Round(f * multiplier)), true
}
