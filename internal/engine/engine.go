package engine

import (
	"context"
	"fmt"
	"os"

	"github.com/sky-engine/internal/config"
	"github.com/sky-engine/internal/ingest/rtmp"
	"github.com/sky-engine/internal/ingest/rtsp"
	"github.com/sky-engine/internal/serve/hls"
	"github.com/sky-engine/internal/transcode"
)

type Engine struct {
	cfg         config.Config
	manager     *transcode.Manager
	rtmpServer  *rtmp.Server
	hlsServer   *hls.Server
	rtspHandler *rtsp.Handler
}

func New(cfg config.Config) *Engine {
	manager := transcode.NewManager(toTranscodeConfig(cfg))
	rtspHandler := rtsp.NewHandler(manager)

	var opts []hls.Option
	if cfg.RTSPIngestAPI {
		opts = append(opts, hls.WithRTSPHandlers(rtspHandler.HandleCreate, rtspHandler.HandleDelete))
	}
	return &Engine{
		cfg:         cfg,
		manager:     manager,
		rtmpServer:  rtmp.New(cfg.RTMPListen, manager),
		hlsServer:   hls.New(cfg.HTTPListen, cfg.StoragePath, opts...),
		rtspHandler: rtspHandler,
	}
}

func (e *Engine) Run(ctx context.Context) error {
	if err := os.MkdirAll(e.cfg.StoragePath, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	httpErr := make(chan error, 1)
	rtmpErr := make(chan error, 1)
	go func() { httpErr <- e.hlsServer.Serve() }()
	go func() { rtmpErr <- e.rtmpServer.Serve(ctx) }()

	select {
	case <-ctx.Done():
		e.manager.StopAll()
		return ctx.Err()
	case err := <-httpErr:
		e.manager.StopAll()
		return err
	case err := <-rtmpErr:
		e.manager.StopAll()
		return err
	}
}

func toTranscodeConfig(cfg config.Config) transcode.Config {
	variants := make([]transcode.Variant, 0, len(cfg.Variants))
	for _, v := range cfg.Variants {
		variants = append(variants, transcode.Variant{
			Name:         v.Name,
			Width:        v.Width,
			Height:       v.Height,
			VideoBitrate: v.VideoBitrate,
			MaxRate:      v.MaxRate,
			BufSize:      v.BufSize,
			AudioBitrate: v.AudioBitrate,
		})
	}

	return transcode.Config{
		FFmpegBin:        cfg.FFmpegBin,
		StoragePath:      cfg.StoragePath,
		SegmentTime:      cfg.SegmentTime,
		SegmentCount:     cfg.SegmentCount,
		MasterName:       cfg.MasterName,
		VideoCodec:       cfg.VideoCodec,
		VideoPreset:      cfg.VideoPreset,
		VideoTune:        cfg.VideoTune,
		VideoFPS:         cfg.VideoFPS,
		AudioCodec:       cfg.AudioCodec,
		AudioSampleRate:  cfg.AudioSampleRate,
		RTSPTransport:    cfg.RTSPTransport,
		RTSPStimeoutUSec: cfg.RTSPStimeoutUSec,
		MaxStreams:       cfg.MaxStreams,
		IdleTimeoutSec:   cfg.IdleTimeoutSec,
		Variants:         variants,
	}
}
