package transcode

import (
	"strings"
	"testing"
)

func TestBuildArgsIncludesABRSettings(t *testing.T) {
	t.Parallel()
	cfg := Config{
		SegmentTime:      2,
		SegmentCount:     10,
		MasterName:       "master.m3u8",
		StoragePath:      "/tmp/hls",
		VideoCodec:       "libx264",
		VideoPreset:      "veryfast",
		VideoTune:        "zerolatency",
		VideoFPS:         25,
		AudioCodec:       "aac",
		AudioSampleRate:  48000,
		RTSPTransport:    "tcp",
		RTSPStimeoutUSec: 5_000_000,
		Variants: []Variant{
			{Name: "360p", Width: 640, Height: 360, VideoBitrate: "800k", MaxRate: "856k", BufSize: "1200k", AudioBitrate: "96k"},
			{Name: "720p", Width: 1280, Height: 720, VideoBitrate: "2800k", MaxRate: "2996k", BufSize: "4200k", AudioBitrate: "128k"},
		},
	}

	args := buildArgs(cfg, "rtsp://localhost/stream", "live/stream1")
	joined := strings.Join(args, " ")

	checks := []string{
		"-fflags +genpts+igndts+discardcorrupt",
		"-flags +low_delay",
		"-var_stream_map v:0,a:0,name:360p v:1,a:1,name:720p",
		"-master_pl_name master.m3u8",
		"-hls_segment_filename /tmp/hls/live/stream1/%v/segment_%06d.ts",
		"-s:v:0 640x360",
		"-s:v:1 1280x720",
		"-rtsp_transport tcp",
		"-stimeout 5000000",
	}
	for _, c := range checks {
		if !strings.Contains(joined, c) {
			t.Fatalf("args missing %q in %s", c, joined)
		}
	}
}

func TestNormalizeRateControl(t *testing.T) {
	t.Parallel()

	maxRate, bufSize := normalizeRateControl("2500k", "2800k", "4200k")
	if maxRate != "3000k" || bufSize != "7500k" {
		t.Fatalf("unexpected normalized rates: maxrate=%s bufsize=%s", maxRate, bufSize)
	}

	maxRate, bufSize = normalizeRateControl("2500k", "3500k", "9000k")
	if maxRate != "3500k" || bufSize != "9000k" {
		t.Fatalf("should preserve already-safe rates: maxrate=%s bufsize=%s", maxRate, bufSize)
	}
}

func TestRTSPInputFlags(t *testing.T) {
	t.Parallel()
	flags := rtspInputFlags("rtsp://192.168.1.10/stream", "tcp", 5_000_000)
	if len(flags) != 4 || flags[0] != "-rtsp_transport" || flags[1] != "tcp" || flags[2] != "-stimeout" || flags[3] != "5000000" {
		t.Fatalf("unexpected rtsp flags: %#v", flags)
	}
	if got := rtspInputFlags("pipe:0", "tcp", 5_000_000); got != nil {
		t.Fatalf("expected nil flags for non-rtsp input, got %#v", got)
	}
}
