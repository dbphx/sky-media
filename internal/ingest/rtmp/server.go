package rtmp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	"github.com/sky-engine/internal/transcode"
	flvtag "github.com/yutopp/go-flv/tag"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"
)

type Server struct {
	listen  string
	manager *transcode.Manager
}

func New(listen string, manager *transcode.Manager) *Server {
	return &Server{listen: listen, manager: manager}
}

func (s *Server) Serve(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.listen)
	if err != nil {
		return fmt.Errorf("listen rtmp: %w", err)
	}

	srv := rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			logger := logrus.New()
			logger.SetOutput(os.Stdout)
			logger.SetLevel(logrus.InfoLevel)
			h := &handler{manager: s.manager}
			return conn, buildConnConfig(h, logger)
		},
	})

	go func() {
		<-ctx.Done()
		_ = srv.Close()
		_ = listener.Close()
	}()

	log.Printf("rtmp ingest serving on %s (path: /{app}/{streamKey})", s.listen)
	err = srv.Serve(listener)
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "closed") {
		return nil
	}
	return err
}

type handler struct {
	rtmp.DefaultHandler
	manager   *transcode.Manager
	app       string
	streamKey string
	stream    *transcode.Stream
	tsBaseSet bool
	tsBase    uint32
	lastTSSet bool
	lastTS    uint32
}

func buildConnConfig(h *handler, logger *logrus.Logger) *rtmp.ConnConfig {
	return &rtmp.ConnConfig{
		Handler:                   h,
		Logger:                    logger,
		SkipHandshakeVerification: true,
	}
}

func (h *handler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	h.app = resolveConnectApp(cmd)
	if cmd == nil {
		log.Printf("rtmp connect app=%s (nil connect command)", h.app)
		return nil
	}
	log.Printf("rtmp connect app=%s raw_app=%s tcurl=%s", h.app, cmd.Command.App, cmd.Command.TCURL)
	return nil
}

func (h *handler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	log.Printf("rtmp publish incoming app=%s publishing_name=%s", h.app, cmd.PublishingName)
	app, key := splitPublishPath(h.app, cmd.PublishingName)
	if app == "" || key == "" {
		log.Printf("rtmp publish rejected invalid path app=%s publishing_name=%s", h.app, cmd.PublishingName)
		return fmt.Errorf("invalid publish path")
	}
	s, err := h.manager.StartRTMP(app, key)
	if err != nil {
		return err
	}
	h.app = app
	h.streamKey = key
	h.stream = s
	log.Printf("rtmp publish accepted app=%s stream_key=%s", h.app, h.streamKey)
	return nil
}

func (h *handler) OnSetDataFrame(ts uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	r := bytes.NewReader(data.Payload)
	var script flvtag.ScriptData
	if err := flvtag.DecodeScriptData(r, &script); err != nil {
		return nil
	}
	return h.stream.EncodeTag(&flvtag.FlvTag{TagType: flvtag.TagTypeScriptData, Timestamp: ts, Data: &script})
}

func (h *handler) OnAudio(ts uint32, payload io.Reader) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	var audio flvtag.AudioData
	if err := flvtag.DecodeAudioData(payload, &audio); err != nil {
		return err
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, audio.Data); err != nil {
		return err
	}
	audio.Data = body
	return h.stream.EncodeTag(&flvtag.FlvTag{TagType: flvtag.TagTypeAudio, Timestamp: ts, Data: &audio})
}

func (h *handler) OnVideo(ts uint32, payload io.Reader) error {
	if h.stream == nil {
		return nil
	}
	ts = h.normalizeTimestamp(ts)
	var video flvtag.VideoData
	if err := flvtag.DecodeVideoData(payload, &video); err != nil {
		return err
	}
	body := new(bytes.Buffer)
	if _, err := io.Copy(body, video.Data); err != nil {
		return err
	}
	video.Data = body
	return h.stream.EncodeTag(&flvtag.FlvTag{TagType: flvtag.TagTypeVideo, Timestamp: ts, Data: &video})
}

func (h *handler) OnClose() {
	if h.app == "" || h.streamKey == "" {
		log.Printf("rtmp close without active stream")
		return
	}
	log.Printf("rtmp close app=%s stream_key=%s", h.app, h.streamKey)
	h.manager.ScheduleStop(h.app, h.streamKey)
}

func splitPublishPath(app string, publishingName string) (string, string) {
	clean := strings.Trim(publishingName, "/")
	if clean == "" {
		return sanitizePathPart(app), ""
	}
	parts := strings.Split(clean, "/")
	if len(parts) >= 2 {
		return sanitizePathPart(parts[0]), sanitizePathPart(parts[1])
	}
	return sanitizePathPart(app), sanitizePathPart(parts[0])
}

func sanitizePathPart(v string) string {
	v = strings.TrimSpace(strings.Trim(v, "/"))
	if v == "" || v == "." || v == ".." || strings.Contains(v, "..") || strings.Contains(v, "\\") {
		return ""
	}
	return v
}

func (h *handler) normalizeTimestamp(ts uint32) uint32 {
	var normalized uint32
	if !h.tsBaseSet {
		h.tsBaseSet = true
		h.tsBase = ts
		normalized = 0
	} else if ts < h.tsBase {
		normalized = 0
	} else {
		normalized = ts - h.tsBase
	}

	if !h.lastTSSet {
		h.lastTSSet = true
		h.lastTS = normalized
		return normalized
	}
	if normalized <= h.lastTS {
		h.lastTS++
		return h.lastTS
	}

	h.lastTS = normalized
	return normalized
}

func resolveConnectApp(cmd *rtmpmsg.NetConnectionConnect) string {
	if cmd == nil {
		return ""
	}

	if app := firstPathPart(cmd.Command.App); app != "" {
		return app
	}

	tcurl := strings.TrimSpace(cmd.Command.TCURL)
	if tcurl == "" {
		return ""
	}
	u, err := url.Parse(tcurl)
	if err != nil {
		return ""
	}
	return firstPathPart(u.Path)
}

func firstPathPart(v string) string {
	clean := strings.TrimSpace(strings.Trim(v, "/"))
	if clean == "" {
		return ""
	}
	parts := strings.Split(clean, "/")
	return sanitizePathPart(parts[0])
}
