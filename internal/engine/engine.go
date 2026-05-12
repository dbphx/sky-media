package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sky-engine/internal/config"
	"github.com/yutopp/go-flv"
	flvtag "github.com/yutopp/go-flv/tag"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

type Engine struct {
	cfg     config.Config
	manager *streamManager
}

func New(cfg config.Config) *Engine {
	return &Engine{cfg: cfg, manager: newStreamManager(cfg)}
}

func (e *Engine) Run(ctx context.Context) error {
	if err := os.MkdirAll(e.cfg.StoragePath, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	httpErr := make(chan error, 1)
	rtmpErr := make(chan error, 1)
	go func() { httpErr <- e.serveHTTP() }()
	go func() { rtmpErr <- e.serveRTMP(ctx) }()

	select {
	case <-ctx.Done():
		e.manager.stopAll()
		return ctx.Err()
	case err := <-httpErr:
		e.manager.stopAll()
		return err
	case err := <-rtmpErr:
		e.manager.stopAll()
		return err
	}
}

func (e *Engine) serveRTMP(ctx context.Context) error {
	listener, err := net.Listen("tcp", e.cfg.RTMPListen)
	if err != nil {
		return fmt.Errorf("listen rtmp: %w", err)
	}

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			logger := logrus.New()
			logger.SetOutput(os.Stdout)
			logger.SetLevel(logrus.InfoLevel)
			h := &rtmpHandler{manager: e.manager}
			return conn, e.buildConnConfig(h, logger)
		},
	})

	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = listener.Close()
	}()

	log.Printf("rtmp ingest serving on %s (path: /{app}/{streamKey})", e.cfg.RTMPListen)
	err = srv.Serve(listener)
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "closed") {
		return nil
	}
	return err
}

func (e *Engine) buildConnConfig(h *rtmpHandler, logger *logrus.Logger) *rtmp.ConnConfig {
	return &rtmp.ConnConfig{
		Handler:                   h,
		Logger:                    logger,
		SkipHandshakeVerification: true,
	}
}

func (e *Engine) buildFFmpegArgs(inputURL string, streamPath string) []string {
	forceKeyFrames := fmt.Sprintf("expr:gte(t,n_forced*%d)", e.cfg.SegmentTime)
	gop := strconv.Itoa(e.cfg.SegmentTime * e.cfg.VideoFPS)
	audioSampleRate := strconv.Itoa(e.cfg.AudioSampleRate)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		// discardcorrupt + low_delay reduce decoder warnings when RTMP drops
		// packets or joins mid-GOP (e.g. "co located POCs unavailable").
		"-fflags", "+genpts+igndts+discardcorrupt",
		"-flags", "+low_delay",
	}
	args = append(args, e.rtspInputFlags(inputURL)...)
	args = append(args, "-i", inputURL)
	for i, v := range e.cfg.Variants {
		maxRate, bufSize := normalizeRateControl(v.VideoBitrate, v.MaxRate, v.BufSize)
		args = append(args,
			"-map", "0:v:0?", "-map", "0:a:0?",
			"-c:v:"+strconv.Itoa(i), e.cfg.VideoCodec,
			"-preset", e.cfg.VideoPreset,
			"-tune", e.cfg.VideoTune,
			"-b:v:"+strconv.Itoa(i), v.VideoBitrate,
			"-maxrate:v:"+strconv.Itoa(i), maxRate,
			"-bufsize:v:"+strconv.Itoa(i), bufSize,
			"-s:v:"+strconv.Itoa(i), fmt.Sprintf("%dx%d", v.Width, v.Height),
			"-g", gop,
			"-keyint_min", gop,
			"-sc_threshold", "0",
			"-force_key_frames", forceKeyFrames,
			"-c:a:"+strconv.Itoa(i), e.cfg.AudioCodec,
			"-b:a:"+strconv.Itoa(i), v.AudioBitrate,
			"-ar", audioSampleRate,
		)
	}

	var streamMap []string
	for i := range e.cfg.Variants {
		streamMap = append(streamMap, fmt.Sprintf("v:%d,a:%d,name:%s", i, i, e.cfg.Variants[i].Name))
	}

	streamRoot := filepath.Join(e.cfg.StoragePath, streamPath)
	segmentPattern := filepath.Join(streamRoot, "%v", "segment_%06d.ts")
	variantPlaylistPattern := filepath.Join(streamRoot, "%v", "index.m3u8")
	_ = os.MkdirAll(streamRoot, 0o755)

	args = append(args,
		"-fps_mode", "passthrough",
		"-af", "aresample=async=1:first_pts=0",
		"-avoid_negative_ts", "make_zero",
		"-max_interleave_delta", "0",
		"-f", "hls",
		"-hls_time", strconv.Itoa(e.cfg.SegmentTime),
		"-hls_list_size", strconv.Itoa(e.cfg.SegmentCount),
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
		"-hls_delete_threshold", "1",
		"-master_pl_name", e.cfg.MasterName,
		"-hls_segment_filename", segmentPattern,
		"-var_stream_map", strings.Join(streamMap, " "),
		variantPlaylistPattern,
	)

	return args
}

func (e *Engine) rtspInputFlags(inputURL string) []string {
	low := strings.ToLower(strings.TrimSpace(inputURL))
	if !strings.HasPrefix(low, "rtsp://") && !strings.HasPrefix(low, "rtsps://") {
		return nil
	}
	var out []string
	if t := strings.TrimSpace(e.cfg.RTSPTransport); t != "" {
		out = append(out, "-rtsp_transport", t)
	}
	if e.cfg.RTSPStimeoutUSec > 0 {
		out = append(out, "-stimeout", strconv.Itoa(e.cfg.RTSPStimeoutUSec))
	}
	return out
}

type streamManager struct {
	cfg     config.Config
	mu      sync.Mutex
	streams map[string]*activeStream
}

type activeStream struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	enc    *flv.Encoder
	cancel context.CancelFunc
	timer  *time.Timer
}

func newStreamManager(cfg config.Config) *streamManager {
	return &streamManager{cfg: cfg, streams: make(map[string]*activeStream)}
}

func (m *streamManager) start(app string, streamKey string, argsBuilder func(string, string) []string) (*activeStream, error) {
	key := filepath.ToSlash(filepath.Join(app, streamKey))

	m.mu.Lock()
	if s, ok := m.streams[key]; ok {
		// If a stream reconnects while its old pipeline is still in idle timeout,
		// restart ffmpeg to avoid DTS regressions caused by timestamp reset.
		if s.timer != nil {
			delete(m.streams, key)
			m.mu.Unlock()
			stopActiveStream(s)
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
	cmd := exec.CommandContext(ctx, m.cfg.FFmpegBin, argsBuilder("pipe:0", key)...)
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

	s := &activeStream{cmd: cmd, stdin: stdin, enc: enc, cancel: cancel}
	m.streams[key] = s
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("ffmpeg exited for %s: %v", key, err)
		}
	}()
	m.mu.Unlock()
	return s, nil
}

func (m *streamManager) scheduleStop(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.streams[key]
	if !ok {
		return
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(time.Duration(m.cfg.IdleTimeoutSec)*time.Second, func() { m.stop(key) })
}

func (m *streamManager) stop(key string) {
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
	stopActiveStream(s)
}

func (m *streamManager) stopAll() {
	m.mu.Lock()
	keys := make([]string, 0, len(m.streams))
	for k := range m.streams {
		keys = append(keys, k)
	}
	m.mu.Unlock()
	for _, k := range keys {
		m.stop(k)
	}
}

func stopActiveStream(s *activeStream) {
	s.cancel()
	if s.stdin != nil {
		_ = s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

type rtmpHandler struct {
	rtmp.DefaultHandler
	manager   *streamManager
	app       string
	streamKey string
	stream    *activeStream
	tsBaseSet bool
	tsBase    uint32
	lastTSSet bool
	lastTS    uint32
}

func (h *rtmpHandler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.app = resolveConnectApp(cmd)
	if cmd == nil {
		log.Printf("rtmp connect app=%s (nil connect command)", h.app)
		return nil
	}
	log.Printf("rtmp connect app=%s raw_app=%s tcurl=%s", h.app, cmd.Command.App, cmd.Command.TCURL)
	return nil
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	log.Printf("rtmp publish incoming app=%s publishing_name=%s", h.app, cmd.PublishingName)
	app, key := splitPublishPath(h.app, cmd.PublishingName)
	if app == "" || key == "" {
		log.Printf("rtmp publish rejected invalid path app=%s publishing_name=%s", h.app, cmd.PublishingName)
		return fmt.Errorf("invalid publish path")
	}
	s, err := h.manager.start(app, key, func(input, streamPath string) []string {
		e := Engine{cfg: h.manager.cfg}
		return e.buildFFmpegArgs(input, streamPath)
	})
	if err != nil {
		return err
	}
	h.app = app
	h.streamKey = key
	h.stream = s
	log.Printf("rtmp publish accepted app=%s stream_key=%s", h.app, h.streamKey)
	return nil
}

func (h *rtmpHandler) OnSetDataFrame(ts uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	r := bytes.NewReader(data.Payload)
	var script flvtag.ScriptData
	if err := flvtag.DecodeScriptData(r, &script); err != nil {
		return nil
	}
	return h.stream.enc.Encode(&flvtag.FlvTag{TagType: flvtag.TagTypeScriptData, Timestamp: ts, Data: &script})
}

func (h *rtmpHandler) OnAudio(ts uint32, payload io.Reader) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	var audio flvtag.AudioData
	if err := flvtag.DecodeAudioData(payload, &audio); err != nil {
		return err
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, audio.Data); err != nil {
		return err
	}
	audio.Data = body
	return h.stream.enc.Encode(&flvtag.FlvTag{TagType: flvtag.TagTypeAudio, Timestamp: ts, Data: &audio})
}

func (h *rtmpHandler) OnVideo(ts uint32, payload io.Reader) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	var video flvtag.VideoData
	if err := flvtag.DecodeVideoData(payload, &video); err != nil {
		return err
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, video.Data); err != nil {
		return err
	}
	video.Data = body
	return h.stream.enc.Encode(&flvtag.FlvTag{TagType: flvtag.TagTypeVideo, Timestamp: ts, Data: &video})
}

func (h *rtmpHandler) OnClose() {
	if h.app == "" || h.streamKey == "" {
		log.Printf("rtmp close without active stream")
		return
	}
	log.Printf("rtmp close app=%s stream_key=%s", h.app, h.streamKey)
	h.manager.scheduleStop(filepath.ToSlash(filepath.Join(h.app, h.streamKey)))
}

func splitPublishPath(app string, publishingName string) (string, string) {
	clean := strings.Trim(publishingName, "/")
	if clean == "" {
		return sanitizePathPart(app), ""
	}
	parts := strings.Split(clean, "/")
	if len(parts) >= 2 {
		return sanitizePathPart(parts[0]), sanitizePathPart(parts[1])
	}
	return sanitizePathPart(app), sanitizePathPart(parts[0])
}

func sanitizePathPart(v string) string {
	v = strings.TrimSpace(strings.Trim(v, "/"))
	if v == "" || v == "." || v == ".." || strings.Contains(v, "..") || strings.Contains(v, "\\") {
		return ""
	}
	return v
}

func (h *rtmpHandler) normalizeTimestamp(ts uint32) uint32 {
	var normalized uint32
	if !h.tsBaseSet {
		h.tsBaseSet = true
		h.tsBase = ts
		normalized = 0
	} else if ts < h.tsBase {
		normalized = 0
	} else {
		normalized = ts - h.tsBase
	}

	if !h.lastTSSet {
		h.lastTSSet = true
		h.lastTS = normalized
		return normalized
	}
	if normalized <= h.lastTS {
		h.lastTS++
		return h.lastTS
	}

	h.lastTS = normalized
	return normalized
}

func resolveConnectApp(cmd *rtmpmsg.NetConnectionConnect) string {
	if cmd == nil {
		return ""
	}

	if app := firstPathPart(cmd.Command.App); app != "" {
		return app
	}

	tcurl := strings.TrimSpace(cmd.Command.TCURL)
	if tcurl == "" {
		return ""
	}
	u, err := url.Parse(tcurl)
	if err != nil {
		return ""
	}
	return firstPathPart(u.Path)
}

func firstPathPart(v string) string {
	clean := strings.TrimSpace(strings.Trim(v, "/"))
	if clean == "" {
		return ""
	}
	parts := strings.Split(clean, "/")
	return sanitizePathPart(parts[0])
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

func (e *Engine) serveHTTP() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(e.cfg.StoragePath)))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if e.cfg.RTSPIngestAPI {
		mux.HandleFunc("/api/ingest/rtsp", e.handleRTSPIngestCreate)
		mux.HandleFunc("/api/ingest/rtsp/", e.handleRTSPIngestDelete)
	}

	log.Printf("http hls serving on %s (path: /)", e.cfg.HTTPListen)
	if e.cfg.RTSPIngestAPI {
		log.Printf("rtsp ingest api: POST /api/ingest/rtsp, DELETE /api/ingest/rtsp/{app}/{stream}")
	}
	return http.ListenAndServe(e.cfg.HTTPListen, mux)
}
