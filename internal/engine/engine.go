package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

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
			h := &rtmpHandler{manager: e.manager}
			return conn, &rtmp.ConnConfig{Handler: h}
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

func (e *Engine) buildFFmpegArgs(inputURL string, streamPath string) []string {
	forceKeyFrames := fmt.Sprintf("expr:gte(t,n_forced*%d)", e.cfg.SegmentTime)
	gop := strconv.Itoa(e.cfg.SegmentTime * e.cfg.VideoFPS)
	audioSampleRate := strconv.Itoa(e.cfg.AudioSampleRate)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts+igndts",
		"-use_wallclock_as_timestamps", "1",
		"-i", inputURL,
	}
	for i, v := range e.cfg.Variants {
		args = append(args,
			"-map", "0:v:0", "-map", "0:a:0",
			"-c:v:"+strconv.Itoa(i), e.cfg.VideoCodec,
			"-preset", e.cfg.VideoPreset,
			"-tune", e.cfg.VideoTune,
			"-b:v:"+strconv.Itoa(i), v.VideoBitrate,
			"-maxrate:v:"+strconv.Itoa(i), v.MaxRate,
			"-bufsize:v:"+strconv.Itoa(i), v.BufSize,
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
		"-vsync", "cfr",
		"-af", "aresample=async=1:first_pts=0",
		"-avoid_negative_ts", "make_zero",
		"-max_interleave_delta", "0",
		"-f", "hls",
		"-hls_time", strconv.Itoa(e.cfg.SegmentTime),
		"-hls_list_size", strconv.Itoa(e.cfg.SegmentCount),
		"-hls_flags", "append_list+independent_segments+omit_endlist",
		"-hls_playlist_type", "event",
		"-master_pl_name", e.cfg.MasterName,
		"-hls_segment_filename", segmentPattern,
		"-var_stream_map", strings.Join(streamMap, " "),
		variantPlaylistPattern,
	)

	return args
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
	defer m.mu.Unlock()
	if s, ok := m.streams[key]; ok {
		if s.timer != nil {
			s.timer.Stop()
			s.timer = nil
		}
		return s, nil
	}
	if len(m.streams) >= m.cfg.MaxStreams {
		return nil, fmt.Errorf("max streams reached")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, m.cfg.FFmpegBin, argsBuilder("pipe:0", key)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create ffmpeg stdin: %w", err)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	enc, err := flv.NewEncoder(stdin, flv.FlagsAudio|flv.FlagsVideo)
	if err != nil {
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
	s.cancel()
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
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

type rtmpHandler struct {
	rtmp.DefaultHandler
	manager   *streamManager
	app       string
	streamKey string
	stream    *activeStream
}

func (h *rtmpHandler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.app = sanitizePathPart(cmd.Command.App)
	return nil
}

func (h *rtmpHandler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	app, key := splitPublishPath(h.app, cmd.PublishingName)
	if app == "" || key == "" {
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
	return nil
}

func (h *rtmpHandler) OnSetDataFrame(ts uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.stream == nil {
		return nil
	}
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
		return
	}
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

func (e *Engine) serveHTTP() error {
	mux := http.NewServeMux()
	mux.Handle("/hls/", http.StripPrefix("/hls/", http.FileServer(http.Dir(e.cfg.StoragePath))))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("http hls serving on %s (path: /hls/)", e.cfg.HTTPListen)
	return http.ListenAndServe(e.cfg.HTTPListen, mux)
}
