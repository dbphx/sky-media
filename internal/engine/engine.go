package engine

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/sky-engine/internal/config"
)

type Engine struct {
	cfg config.Config
}

func New(cfg config.Config) *Engine {
	return &Engine{cfg: cfg}
}

func (e *Engine) Run(ctx context.Context) error {
	if err := os.MkdirAll(e.cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ffmpegCmd, err := e.startFFmpeg(ctx)
	if err != nil {
		return err
	}

	httpErr := make(chan error, 1)
	go func() {
		httpErr <- e.serveHTTP()
	}()

	select {
	case <-ctx.Done():
		_ = ffmpegCmd.Process.Kill()
		return ctx.Err()
	case err := <-httpErr:
		_ = ffmpegCmd.Process.Kill()
		return err
	}
}

func (e *Engine) startFFmpeg(ctx context.Context) (*exec.Cmd, error) {
	rtmpURL := fmt.Sprintf("rtmp://0.0.0.0%s/%s/%s", e.cfg.RTMPListen, e.cfg.RTMPApp, e.cfg.RTMPStream)
	playlistPath := filepath.Join(e.cfg.OutputDir, e.cfg.MasterName)
	segmentPattern := filepath.Join(e.cfg.OutputDir, "segment_%06d.ts")

	forceKeyFrames := fmt.Sprintf("expr:gte(t,n_forced*%d)", e.cfg.SegmentTime)
	gop := fmt.Sprintf("%d", e.cfg.SegmentTime*e.cfg.VideoFPS)
	audioSampleRate := fmt.Sprintf("%d", e.cfg.AudioSampleRate)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-listen", "1",
		"-i", rtmpURL,
		"-c:v", e.cfg.VideoCodec,
		"-preset", e.cfg.VideoPreset,
		"-tune", e.cfg.VideoTune,
		"-g", gop,
		"-keyint_min", gop,
		"-sc_threshold", "0",
		"-force_key_frames", forceKeyFrames,
		"-c:a", e.cfg.AudioCodec,
		"-b:a", e.cfg.AudioBitrate,
		"-ar", audioSampleRate,
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", e.cfg.SegmentTime),
		"-hls_list_size", fmt.Sprintf("%d", e.cfg.SegmentCount),
		"-hls_flags", "append_list+independent_segments+omit_endlist",
		"-hls_playlist_type", "event",
		"-hls_segment_filename", segmentPattern,
		playlistPath,
	}

	cmd := exec.CommandContext(ctx, e.cfg.FFmpegBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			log.Printf("ffmpeg exited: %v", err)
		}
	}()

	log.Printf("ffmpeg listening for RTMP at %s", rtmpURL)
	return cmd, nil
}

func (e *Engine) serveHTTP() error {
	mux := http.NewServeMux()
	mux.Handle("/hls/", http.StripPrefix("/hls/", http.FileServer(http.Dir(e.cfg.OutputDir))))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("http hls serving on %s (path: /hls/)", e.cfg.HTTPListen)
	return http.ListenAndServe(e.cfg.HTTPListen, mux)
}
