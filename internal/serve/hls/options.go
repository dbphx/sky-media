package hls

import "net/http"

type Option func(*Server)

func WithRTSPHandlers(create http.HandlerFunc, delete http.HandlerFunc) Option {
	return func(s *Server) {
		s.rtspEnabled = true
		s.rtspCreate = create
		s.rtspDelete = delete
	}
}
