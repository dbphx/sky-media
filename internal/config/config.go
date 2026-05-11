package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Variant struct {
	Name         string `yaml:"name"`
	Width        int    `yaml:"width"`
	Height       int    `yaml:"height"`
	VideoBitrate string `yaml:"video_bitrate"`
	MaxRate      string `yaml:"maxrate"`
	BufSize      string `yaml:"bufsize"`
	AudioBitrate string `yaml:"audio_bitrate"`
}

type Config struct {
	RTMPListen      string    `yaml:"rtmp_listen"`
	HTTPListen      string    `yaml:"http_listen"`
	MaxStreams      int       `yaml:"max_streams"`
	IdleTimeoutSec  int       `yaml:"idle_timeout_sec"`
	StorageMode     string    `yaml:"storage_mode"`
	StoragePath     string    `yaml:"storage_path"`
	MasterName      string    `yaml:"master_name"`
	SegmentTime     int       `yaml:"segment_time"`
	SegmentCount    int       `yaml:"segment_count"`
	FFmpegBin       string    `yaml:"ffmpeg_bin"`
	VideoCodec      string    `yaml:"video_codec"`
	VideoPreset     string    `yaml:"video_preset"`
	VideoTune       string    `yaml:"video_tune"`
	VideoFPS        int       `yaml:"video_fps"`
	AudioCodec      string    `yaml:"audio_codec"`
	AudioBitrate    string    `yaml:"audio_bitrate"`
	AudioSampleRate int       `yaml:"audio_sample_rate"`
	Variants        []Variant `yaml:"variants"`
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
	if c.HTTPListen == "" {
		c.HTTPListen = ":8080"
	}
	if c.MaxStreams <= 0 {
		c.MaxStreams = 100
	}
	if c.IdleTimeoutSec <= 0 {
		c.IdleTimeoutSec = 30
	}
	if c.StorageMode == "" {
		c.StorageMode = "memory"
	}
	if c.StoragePath == "" {
		c.StoragePath = "/tmp/hls"
	}
	if c.MasterName == "" {
		c.MasterName = "master.m3u8"
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
	if len(c.Variants) == 0 {
		c.Variants = []Variant{
			{Name: "360p", Width: 640, Height: 360, VideoBitrate: "800k", MaxRate: "856k", BufSize: "1200k", AudioBitrate: "96k"},
			{Name: "720p", Width: 1280, Height: 720, VideoBitrate: "2800k", MaxRate: "2996k", BufSize: "4200k", AudioBitrate: "128k"},
		}
	}
	for i := range c.Variants {
		if c.Variants[i].AudioBitrate == "" {
			c.Variants[i].AudioBitrate = c.AudioBitrate
		}
	}
}

func (c Config) Validate() error {
	if c.RTMPListen == "" || c.HTTPListen == "" {
		return fmt.Errorf("rtmp_listen, http_listen are required")
	}
	if c.MaxStreams <= 0 {
		return fmt.Errorf("max_streams must be > 0")
	}
	if c.IdleTimeoutSec <= 0 {
		return fmt.Errorf("idle_timeout_sec must be > 0")
	}
	if c.StorageMode != "memory" && c.StorageMode != "disk" {
		return fmt.Errorf("storage_mode must be memory or disk")
	}
	if c.StoragePath == "" {
		return fmt.Errorf("storage_path is required")
	}
	if c.VideoFPS <= 0 {
		return fmt.Errorf("video_fps must be > 0")
	}
	if c.AudioSampleRate <= 0 {
		return fmt.Errorf("audio_sample_rate must be > 0")
	}
	if len(c.Variants) == 0 {
		return fmt.Errorf("variants must not be empty")
	}
	for i, v := range c.Variants {
		if v.Name == "" {
			return fmt.Errorf("variants[%d].name is required", i)
		}
		if v.Width <= 0 || v.Height <= 0 {
			return fmt.Errorf("variants[%d].width/height must be > 0", i)
		}
		if v.VideoBitrate == "" || v.MaxRate == "" || v.BufSize == "" {
			return fmt.Errorf("variants[%d].video_bitrate/maxrate/bufsize are required", i)
		}
	}
	return nil
}
