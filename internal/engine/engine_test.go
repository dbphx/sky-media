package engine

import (
	"strings"
	"testing"

	"github.com/sky-engine/internal/config"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

func TestBuildFFmpegArgsIncludesABRSettings(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		SegmentTime:     2,
		SegmentCount:    10,
		MasterName:      "master.m3u8",
		StoragePath:     "/tmp/hls",
		VideoCodec:      "libx264",
		VideoPreset:     "veryfast",
		VideoTune:       "zerolatency",
		VideoFPS:        25,
		AudioCodec:      "aac",
		AudioSampleRate: 48000,
		Variants: []config.Variant{
			{Name: "360p", Width: 640, Height: 360, VideoBitrate: "800k", MaxRate: "856k", BufSize: "1200k", AudioBitrate: "96k"},
			{Name: "720p", Width: 1280, Height: 720, VideoBitrate: "2800k", MaxRate: "2996k", BufSize: "4200k", AudioBitrate: "128k"},
		},
	}

	e := New(cfg)
	args := e.buildFFmpegArgs("pipe:0", "live/stream1")
	joined := strings.Join(args, " ")

	checks := []string{
		"-var_stream_map v:0,a:0,name:360p v:1,a:1,name:720p",
		"-master_pl_name master.m3u8",
		"-hls_segment_filename /tmp/hls/live/stream1/%v/segment_%06d.ts",
		"-s:v:0 640x360",
		"-s:v:1 1280x720",
	}
	for _, c := range checks {
		if !strings.Contains(joined, c) {
			t.Fatalf("args missing %q in %s", c, joined)
		}
	}
}

func TestSplitPublishPath(t *testing.T) {
	t.Parallel()
	app, key := splitPublishPath("live", "stream1")
	if app != "live" || key != "stream1" {
		t.Fatalf("unexpected split: %s %s", app, key)
	}
	app, key = splitPublishPath("", "app2/stream2")
	if app != "app2" || key != "stream2" {
		t.Fatalf("unexpected split with full path: %s %s", app, key)
	}
}

func TestResolveConnectApp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  *rtmpmsg.NetConnectionConnect
		want string
	}{
		{
			name: "uses app when present",
			cmd: &rtmpmsg.NetConnectionConnect{
				Command: rtmpmsg.NetConnectionConnectCommand{
					App: "ducbph-mtx",
				},
			},
			want: "ducbph-mtx",
		},
		{
			name: "extracts app from tcurl when app is empty",
			cmd: &rtmpmsg.NetConnectionConnect{
				Command: rtmpmsg.NetConnectionConnectCommand{
					TCURL: "rtmp://10.155.2.18:19035/ducbph-mtx",
				},
			},
			want: "ducbph-mtx",
		},
		{
			name: "extracts first path segment from tcurl",
			cmd: &rtmpmsg.NetConnectionConnect{
				Command: rtmpmsg.NetConnectionConnectCommand{
					TCURL: "rtmp://instream.media.insky.io.vn:1935/ducbph-mtx/abc",
				},
			},
			want: "ducbph-mtx",
		},
		{
			name: "returns empty for nil command",
			cmd:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveConnectApp(tt.cmd)
			if got != tt.want {
				t.Fatalf("resolveConnectApp() = %q, want %q", got, tt.want)
			}
		})
	}
}
