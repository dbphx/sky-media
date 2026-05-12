package rtsp

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/sky-engine/internal/transcode"
)

type Handler struct {
	manager *transcode.Manager
}

type ingestRequest struct {
	App    string `json:"app"`
	Stream string `json:"stream"`
	URL    string `json:"url"`
}

func NewHandler(manager *transcode.Manager) *Handler {
	return &Handler{manager: manager}
}

func (h *Handler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req ingestRequest
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

	if _, err := h.manager.StartPull(app, streamKey, raw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	key := filepath.ToSlash(filepath.Join(app, streamKey))
	log.Printf("rtsp ingest started app=%s stream_key=%s", app, streamKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": key})
}

func (h *Handler) HandleDelete(w http.ResponseWriter, r *http.Request) {
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
	h.manager.Stop(app, streamKey)
	log.Printf("rtsp ingest stopped app=%s stream_key=%s", app, streamKey)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func sanitizePathPart(v string) string {
	v = strings.TrimSpace(strings.Trim(v, "/"))
	if v == "" || v == "." || v == ".." || strings.Contains(v, "..") || strings.Contains(v, "\\") {
		return ""
	}
	return v
}
