package hls

import (
	"log"
	"net/http"
	"path/filepath"
	"strings"
)

type Server struct {
	listen      string
	storagePath string
	rtspEnabled bool
	rtspCreate  http.HandlerFunc
	rtspDelete  http.HandlerFunc
}

func New(listen string, storagePath string, opts ...Option) *Server {
	s := &Server{
		listen:      listen,
		storagePath: storagePath,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Server) Serve() error {
	mux := http.NewServeMux()
	mux.Handle("/", hlsFileServer(s.storagePath))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if s.rtspEnabled {
		mux.HandleFunc("/api/ingest/rtsp", s.rtspCreate)
		mux.HandleFunc("/api/ingest/rtsp/", s.rtspDelete)
	}

	log.Printf("http hls serving on %s (path: /)", s.listen)
	if s.rtspEnabled {
		log.Printf("rtsp ingest api: POST /api/ingest/rtsp, DELETE /api/ingest/rtsp/{app}/{stream}")
	}
	return http.ListenAndServe(s.listen, mux)
}

func hlsFileServer(storagePath string) http.Handler {
	files := http.FileServer(http.Dir(storagePath))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Range")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		switch strings.ToLower(filepath.Ext(r.URL.Path)) {
		case ".m3u8":
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		case ".ts":
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
		files.ServeHTTP(w, r)
	})
}
