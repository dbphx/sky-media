package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	RTMPListen      string `yaml:"rtmp_listen"`
	RTMPApp         string `yaml:"rtmp_app"`
	RTMPStream      string `yaml:"rtmp_stream"`
	HTTPListen      string `yaml:"http_listen"`
	OutputDir       string `yaml:"output_dir"`
	MasterName      string `yaml:"master_name"`
	SegmentTime     int    `yaml:"segment_time"`
	SegmentCount    int    `yaml:"segment_count"`
	FFmpegBin       string `yaml:"ffmpeg_bin"`
	VideoCodec      string `yaml:"video_codec"`
	VideoPreset     string `yaml:"video_preset"`
	VideoTune       string `yaml:"video_tune"`
	VideoFPS        int    `yaml:"video_fps"`
	AudioCodec      string `yaml:"audio_codec"`
	AudioBitrate    string `yaml:"audio_bitrate"`
	AudioSampleRate int    `yaml:"audio_sample_rate"`
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.RTMPListen == "" {
		c.RTMPListen = ":1935"
	}
	if c.RTMPApp == "" {
		c.RTMPApp = "live"
	}
	if c.RTMPStream == "" {
		c.RTMPStream = "stream"
	}
	if c.HTTPListen == "" {
		c.HTTPListen = ":8080"
	}
	if c.OutputDir == "" {
		c.OutputDir = "./data/hls"
	}
	if c.MasterName == "" {
		c.MasterName = "index.m3u8"
	}
	if c.SegmentTime <= 0 {
		c.SegmentTime = 2
	}
	if c.SegmentCount <= 0 {
		c.SegmentCount = 6
	}
	if c.FFmpegBin == "" {
		c.FFmpegBin = "ffmpeg"
	}
	if c.VideoCodec == "" {
		c.VideoCodec = "libx264"
	}
	if c.VideoPreset == "" {
		c.VideoPreset = "veryfast"
	}
	if c.VideoTune == "" {
		c.VideoTune = "zerolatency"
	}
	if c.VideoFPS <= 0 {
		c.VideoFPS = 25
	}
	if c.AudioCodec == "" {
		c.AudioCodec = "aac"
	}
	if c.AudioBitrate == "" {
		c.AudioBitrate = "128k"
	}
	if c.AudioSampleRate <= 0 {
		c.AudioSampleRate = 48000
	}
}

func (c Config) Validate() error {
	if c.RTMPListen == "" || c.HTTPListen == "" || c.OutputDir == "" {
		return fmt.Errorf("rtmp_listen, http_listen, output_dir are required")
	}
	if c.VideoFPS <= 0 {
		return fmt.Errorf("video_fps must be > 0")
	}
	if c.AudioSampleRate <= 0 {
		return fmt.Errorf("audio_sample_rate must be > 0")
	}
	return nil
}
