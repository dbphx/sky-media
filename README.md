# Sky Media Engine

Sky Media Engine is a focused Go media service for ingesting live streams and publishing low-latency HLS output. It is intentionally scoped for simple operation:

- RTMP push ingest at `/{app}/{streamKey}`
- optional RTSP pull ingest through an HTTP API
- FFmpeg-based transcoding into ABR HLS variants
- static HLS serving over HTTP
- memory or disk-backed HLS segment storage

For design notes, see:

- [RTMP to HLS design](docs/media-engine-rtmp-to-hls.md)
- [Stage 1 ABR notes](docs/stage1-abr.md)
- [Stage 2 multi-stream notes](docs/stage2-multistream.md)

## Features

- Loads runtime configuration from YAML.
- Accepts RTMP publishers on `rtmp://<host>:1935/{app}/{streamKey}`.
- Starts one FFmpeg pipeline per active stream.
- Produces a configurable HLS master playlist and variant playlists.
- Serves HLS files from the configured storage path.
- Provides `GET /healthz`.
- Optionally exposes RTSP pull ingest endpoints:
  - `POST /api/ingest/rtsp`
  - `DELETE /api/ingest/rtsp/{app}/{stream}`

## Project Structure

- `cmd/engine/main.go`: service entrypoint.
- `internal/config/config.go`: config model, defaults, loading, and validation.
- `internal/engine/engine.go`: service wiring for RTMP ingest, RTSP ingest, transcoding, and HLS serving.
- `internal/ingest/rtmp`: RTMP ingest server.
- `internal/ingest/rtsp`: optional RTSP pull API.
- `internal/transcode`: FFmpeg pipeline and stream manager.
- `internal/serve/hls`: HTTP HLS file server and health endpoint.
- `config/config.yaml`: default runtime config.
- `docs/`: design and implementation notes.

## Requirements

- Go 1.25 or newer
- FFmpeg available in `PATH`
- Docker and Docker Compose, if running in containers

## Configuration

The default config lives at [config/config.yaml](config/config.yaml):

```yaml
rtmp_listen: ":1935"
http_listen: ":8080"
rtsp_ingest_api: false
rtsp_transport: "tcp"
rtsp_stimeout_usec: 5000000
max_streams: 100
idle_timeout_sec: 30
storage_mode: "memory"
storage_path: "/dev/shm/hls"
master_name: "master.m3u8"
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
variants:
  - name: "360p"
    width: 640
    height: 360
    video_bitrate: "800k"
    maxrate: "856k"
    bufsize: "1200k"
    audio_bitrate: "96k"
  - name: "720p"
    width: 1280
    height: 720
    video_bitrate: "2800k"
    maxrate: "2996k"
    bufsize: "4200k"
    audio_bitrate: "128k"
```

Storage options:

- `storage_mode: "memory"` stores HLS output on a memory-backed path such as `/dev/shm/hls`.
- `storage_mode: "disk"` stores HLS output on a persistent path such as `/data/hls`.
- Docker Compose already mounts both `./data:/data` and `/dev/shm/hls` as tmpfs, so switching storage only requires changing `storage_mode` and `storage_path`.

## Run Locally

```bash
go run ./cmd/engine -config ./config/config.yaml
```

The service starts:

- RTMP ingest on `localhost:1935`
- HTTP HLS and health endpoints on `localhost:8080`

## Run With Docker Compose

```bash
docker compose up --build -d
docker compose logs -f media-engine
```

Stop the service:

```bash
docker compose down
```

## RTMP Ingest

Push a stream with FFmpeg:

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream1
```

Push another stream:

```bash
ffmpeg -re -stream_loop -1 -i input2.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream2
```

OBS can publish to the same format:

- Server: `rtmp://localhost:1935/live`
- Stream key: `stream1`

## RTSP Pull Ingest

RTSP ingest is disabled by default. Enable it in config:

```yaml
rtsp_ingest_api: true
```

Start pulling an RTSP source:

```bash
curl -X POST http://localhost:8080/api/ingest/rtsp \
  -H 'Content-Type: application/json' \
  -d '{"app":"camera","stream":"cam1","url":"rtsp://user:pass@camera-host/stream"}'
```

Stop the RTSP pull:

```bash
curl -X DELETE http://localhost:8080/api/ingest/rtsp/camera/cam1
```

## HLS Playback

HLS files are served from the HTTP root using the same `{app}/{streamKey}` path as ingest.

Examples:

- Master playlist: `http://localhost:8080/live/stream1/master.m3u8`
- 360p playlist: `http://localhost:8080/live/stream1/360p/index.m3u8`
- 720p playlist: `http://localhost:8080/live/stream1/720p/index.m3u8`
- RTSP pull example: `http://localhost:8080/camera/cam1/master.m3u8`

Health check:

```bash
curl http://localhost:8080/healthz
```

## Tests

```bash
go test ./...
```

## Operational Notes

- For stable live playback, set the publisher keyframe interval to match `segment_time`; the default is `2` seconds.
- GOP size is computed from `segment_time * video_fps`.
- Each active stream writes to `storage_path/{app}/{streamKey}`.
- The FFmpeg pipeline includes timestamp normalization flags to reduce reconnect issues such as non-monotonic DTS.
- `max_streams` limits the number of concurrently active pipelines.
- `idle_timeout_sec` controls cleanup for idle stream sessions.
