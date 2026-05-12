# RTMP/STMP -> HLS Media Engine

A focused Go media engine inspired by MediaMTX, scoped to:
- ingest RTMP/STMP only
- convert to HLS
- prioritize high performance and simple operation

For full design details, see `docs/media-engine-rtmp-to-hls.md`.
For Stage-1 ABR notes, see `docs/stage1-abr.md`.
For dynamic multi-stream notes, see `docs/stage2-multistream.md`.

## What is implemented

- Go service that loads config from YAML.
- Opens RTMP ingest endpoint with dynamic path `/{app}/{streamKey}`.
- Spawns one FFmpeg pipeline per published stream and produces ABR HLS variants.
- Serves HLS over HTTP.
- Provides `GET /healthz`.

## Project structure

- `cmd/engine/main.go`: app entrypoint.
- `internal/config/config.go`: config model + loader + defaults.
- `internal/engine/engine.go`: FFmpeg pipeline + HTTP serving.
- `internal/config/config_test.go`: unit tests for config loading/validation.
- `internal/transcode/pipeline_test.go`: unit tests for FFmpeg args generation.
- `internal/ingest/rtmp/server_test.go`: unit tests for RTMP ingest parsing.
- `config/config.yaml`: runtime config.

## Requirements

- Go 1.25+
- FFmpeg installed and available in PATH

## Config

Example in `config/config.yaml`:

```yaml
rtmp_listen: ":1935"
http_listen: ":8080"
max_streams: 100
idle_timeout_sec: 30
storage_mode: "disk"
storage_path: "/data/hls"
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

RTMP ingest URL format from this config:
`rtmp://<host>:1935/{app}/{streamKey}`

HLS playback URLs:
- Stream1 master: `http://localhost:8080/live/stream1/master.m3u8`
- Stream2 master: `http://localhost:8080/live/stream2/master.m3u8`
- Stream1 360p: `http://localhost:8080/live/stream1/360p/index.m3u8`
- Stream1 720p: `http://localhost:8080/live/stream1/720p/index.m3u8`

Storage options:
- `storage_mode`: `memory` or `disk` (default `memory`).
- `storage_path`: output path used by engine.
- For `memory` mode, mount `storage_path` as tmpfs in container runtime.
- For `disk` mode, mount `storage_path` to persistent bind/named volume.

Compose is preconfigured for config-only switching:
- Disk mode path: `/data/hls`
- Memory mode path: `/dev/shm/hls`
- Switch by changing only `storage_mode` and `storage_path` in `config/config.yaml`.

## Run (local)

```bash
go run ./cmd/engine -config ./config/config.yaml
```

## Run with Docker Compose

```bash
docker compose up --build -d
docker compose logs -f media-engine
```

## Test with OBS or FFmpeg push

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream1

ffmpeg -re -stream_loop -1 -i input2.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream2
```

## Unit test

```bash
go test ./...
```

## Notes

- For stable live playback, set OBS keyframe interval to `2` seconds (matching `segment_time`).
- GOP is computed from `segment_time * video_fps`.
- Each active stream gets its own output tree under `storage_path/{app}/{streamKey}`.
- FFmpeg includes timestamp normalization flags to reduce `Non-monotonous DTS` on reconnects.
