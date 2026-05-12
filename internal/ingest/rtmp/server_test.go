package rtmp

import (
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

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

func TestBuildConnConfigSkipsHandshakeVerification(t *testing.T) {
	t.Parallel()

	logger := logrus.New()
	cfg := buildConnConfig(&handler{}, logger)
	if cfg == nil {
		t.Fatal("buildConnConfig() returned nil")
	}
	if !cfg.SkipHandshakeVerification {
		t.Fatal("expected SkipHandshakeVerification to be true for SRS compatibility")
	}
}

func TestNormalizeTimestamp(t *testing.T) {
	t.Parallel()

	h := &handler{}
	if got := h.normalizeTimestamp(10286703); got != 0 {
		t.Fatalf("first timestamp should normalize to 0, got %d", got)
	}
	if got := h.normalizeTimestamp(10286736); got != 33 {
		t.Fatalf("expected normalized delta 33, got %d", got)
	}
	if got := h.normalizeTimestamp(10286600); got != 34 {
		t.Fatalf("timestamp smaller than base should be forced monotonic, got %d", got)
	}
	if got := h.normalizeTimestamp(10286700); got != 35 {
		t.Fatalf("timestamp lower than last output should still move forward, got %d", got)
	}
}

func TestSanitizePathPart(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{" live ", "live"},
		{"../secret", ""},
		{"..", ""},
		{"", ""},
		{"app/stream", "app/stream"},
		{"app\\stream", ""},
	}
	for _, tc := range cases {
		if got := sanitizePathPart(tc.input); got != tc.want {
			t.Fatalf("sanitizePathPart(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSplitPublishPathUsesApp(t *testing.T) {
	t.Parallel()
	app, key := splitPublishPath("live", "")
	if app != "live" || key != "" {
		t.Fatalf("expected app live with empty key, got %q %q", app, key)
	}
}

func TestResolveConnectAppUsesPath(t *testing.T) {
	t.Parallel()
	cmd := &rtmpmsg.NetConnectionConnect{Command: rtmpmsg.NetConnectionConnectCommand{TCURL: "rtmp://host/live/stream"}}
	if got := resolveConnectApp(cmd); got != "live" {
		t.Fatalf("expected live, got %s", got)
	}
}

func TestSplitPublishPathSanitize(t *testing.T) {
	t.Parallel()
	app, key := splitPublishPath("..", "../stream")
	if strings.Contains(app, "..") || strings.Contains(key, "..") {
		t.Fatalf("expected sanitized path parts, got %q %q", app, key)
	}
}
