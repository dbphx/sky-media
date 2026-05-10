# RTMP/STMP -> HLS Media Engine

A focused Go media engine inspired by MediaMTX, scoped to:
- ingest RTMP/STMP only
- convert to HLS
- prioritize high performance and simple operation

For full design details, see `docs/media-engine-rtmp-to-hls.md`.

## What is implemented

- Go service that loads config from YAML.
- Opens RTMP ingest endpoint (single configured app/stream path).
- Runs FFmpeg in listen mode to ingest RTMP and produce HLS segments.
- Serves HLS over HTTP.
- Provides `GET /healthz`.

## Project structure

- `cmd/engine/main.go`: app entrypoint.
- `internal/config/config.go`: config model + loader + defaults.
- `internal/engine/engine.go`: FFmpeg pipeline + HTTP serving.
- `config/config.yaml`: runtime config.

## Requirements

- Go 1.25+
- FFmpeg installed and available in PATH

## Config

Example in `config/config.yaml`:

```yaml
rtmp_listen: ":1935"
rtmp_app: "live"
rtmp_stream: "stream"
http_listen: ":8080"
output_dir: "/data/hls"
master_name: "index.m3u8"
segment_time: 2
segment_count: 20
ffmpeg_bin: "ffmpeg"
video_codec: "libx264"
video_preset: "veryfast"
video_tune: "zerolatency"
video_fps: 25
audio_codec: "aac"
audio_bitrate: "128k"
audio_sample_rate: 48000
```

RTMP ingest URL from this config:
`rtmp://0.0.0.0:1935/live/stream`

HLS playback URL:
`http://localhost:8080/hls/index.m3u8`

## Run (local)

```bash
go run ./cmd/engine -config ./config/config.yaml
```

## Run with Docker Compose

Start service:

```bash
docker compose up --build -d
```

View logs:

```bash
docker compose logs -f media-engine
```

Stop service:

```bash
docker compose down
```

## Test with OBS or FFmpeg push

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream
```

Then open:
- `http://localhost:8080/hls/index.m3u8`

## Notes

- For stable live playback, set OBS Keyframe Interval to `2` seconds (matching `segment_time`).
- HLS flags include `omit_endlist` to avoid closing live playlists while streaming.
- GOP is computed from `segment_time * video_fps`; keep OBS FPS aligned with `video_fps` for best segment stability.


- Current implementation transcodes to stable HLS output (`libx264` + `aac`) using config values.
- You can tune codec/preset/fps/bitrate directly in `config/config.yaml`.
- This is MVP and currently supports a single configured stream endpoint.

## Next steps

- Add multi-stream routing by stream key/path.
- Add codec validation and auto fallback transcode profile.
- Add Prometheus metrics and graceful FFmpeg restart policy.
