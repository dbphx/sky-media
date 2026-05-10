package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/sky-engine/internal/config"
	"github.com/sky-engine/internal/engine"
)

func main() {
	cfgPath := flag.String("config", "./config/config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	log.Printf("loaded config: rtmp_listen=%s rtmp_app=%s rtmp_stream=%s http_listen=%s output_dir=%s master_name=%s segment_time=%d segment_count=%d ffmpeg_bin=%s video_codec=%s video_preset=%s video_tune=%s video_fps=%d audio_codec=%s audio_bitrate=%s audio_sample_rate=%d",
		cfg.RTMPListen,
		cfg.RTMPApp,
		cfg.RTMPStream,
		cfg.HTTPListen,
		cfg.OutputDir,
		cfg.MasterName,
		cfg.SegmentTime,
		cfg.SegmentCount,
		cfg.FFmpegBin,
		cfg.VideoCodec,
		cfg.VideoPreset,
		cfg.VideoTune,
		cfg.VideoFPS,
		cfg.AudioCodec,
		cfg.AudioBitrate,
		cfg.AudioSampleRate,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	eng := engine.New(cfg)
	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("engine stopped with error: %v", err)
	}
}
