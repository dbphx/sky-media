package hls

import (
	"log"
	"net/http"
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
	mux.Handle("/", http.FileServer(http.Dir(s.storagePath)))
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
