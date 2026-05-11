package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultsAndVariants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("rtmp_listen: \":1935\"\nhttp_listen: \":8080\"\nstorage_path: \"/tmp/hls\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Variants) == 0 {
		t.Fatalf("expected default variants")
	}
	if cfg.MasterName != "master.m3u8" {
		t.Fatalf("expected default master name, got %s", cfg.MasterName)
	}
	if cfg.MaxStreams <= 0 || cfg.IdleTimeoutSec <= 0 {
		t.Fatalf("expected positive stream defaults")
	}
}

func TestLoadValidatesVariantFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := "rtmp_listen: \":1935\"\nhttp_listen: \":8080\"\nstorage_path: \"/tmp/hls\"\nvariants:\n  - name: \"broken\"\n    width: 0\n    height: 720\n    video_bitrate: \"2000k\"\n    maxrate: \"2200k\"\n    bufsize: \"3000k\"\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := Load(cfgPath); err == nil {
		t.Fatalf("expected validation error")
	}
}
