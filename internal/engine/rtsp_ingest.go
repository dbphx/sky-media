package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type rtspIngestRequest struct {
	App    string `json:"app"`
	Stream string `json:"stream"`
	URL    string `json:"url"`
}

func (e *Engine) handleRTSPIngestCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req rtspIngestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	app := sanitizePathPart(req.App)
	streamKey := sanitizePathPart(req.Stream)
	raw := strings.TrimSpace(req.URL)
	if app == "" || streamKey == "" || raw == "" {
		http.Error(w, "app, stream, and url are required", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "rtsp", "rtsps":
	default:
		http.Error(w, "url scheme must be rtsp or rtsps", http.StatusBadRequest)
		return
	}

	if _, err := e.manager.startPull(context.Background(), app, streamKey, func(streamPath string) []string {
		return e.buildFFmpegArgs(raw, streamPath)
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := filepath.ToSlash(filepath.Join(app, streamKey))
	log.Printf("rtsp ingest started app=%s stream_key=%s", app, streamKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": key})
}

func (e *Engine) handleRTSPIngestDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/ingest/rtsp/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		http.Error(w, "missing path after /api/ingest/rtsp/", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "path must be {app}/{stream}", http.StatusBadRequest)
		return
	}
	app := sanitizePathPart(parts[0])
	streamKey := sanitizePathPart(parts[1])
	if app == "" || streamKey == "" {
		http.Error(w, "invalid app or stream", http.StatusBadRequest)
		return
	}
	key := filepath.ToSlash(filepath.Join(app, streamKey))
	e.manager.stop(key)
	log.Printf("rtsp ingest stopped path=%s", key)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (m *streamManager) startPull(_ context.Context, app, streamKey string, argsBuilder func(streamPath string) []string) (*activeStream, error) {
	key := filepath.ToSlash(filepath.Join(app, streamKey))

	m.mu.Lock()
	if s, ok := m.streams[key]; ok {
		delete(m.streams, key)
		m.mu.Unlock()
		stopActiveStream(s)
		m.mu.Lock()
	}
	if len(m.streams) >= m.cfg.MaxStreams {
		m.mu.Unlock()
		return nil, fmt.Errorf("max streams reached")
	}

	ctx, cancel := context.WithCancel(context.Background())
	args := argsBuilder(key)
	cmd := exec.CommandContext(ctx, m.cfg.FFmpegBin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		m.mu.Unlock()
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	s := &activeStream{cmd: cmd, stdin: nil, enc: nil, cancel: cancel}
	m.streams[key] = s
	go func(snap *activeStream, c *exec.Cmd) {
		if err := c.Wait(); err != nil {
			log.Printf("ffmpeg exited for %s: %v", key, err)
		}
		// Pull pipelines have no RTMP OnClose to evict dead entries.
		m.mu.Lock()
		defer m.mu.Unlock()
		cur, ok := m.streams[key]
		if !ok || cur != snap {
			return
		}
		if cur.timer != nil {
			cur.timer.Stop()
		}
		delete(m.streams, key)
	}(s, cmd)
	m.mu.Unlock()
	return s, nil
}
