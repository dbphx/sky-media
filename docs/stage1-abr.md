# Stage 1 - ABR from Config

This stage adds multi-resolution HLS output from a single RTMP input endpoint.

## Input

- RTMP ingest URL: `rtmp://<host>:1935/live/stream`

## Output

- HLS master playlist: `http://<host>:8080/hls/master.m3u8`
- Variant playlists:
  - `http://<host>:8080/hls/360p/index.m3u8`
  - `http://<host>:8080/hls/720p/index.m3u8`

## Config Fields

Core fields:
- `segment_time`: target segment duration in seconds
- `segment_count`: sliding window size in segments
- `master_name`: master playlist file name

ABR fields:
- `variants[]`: list of output renditions
  - `name`: variant name used in output directory and playlist labels
  - `width`, `height`: target resolution
  - `video_bitrate`, `maxrate`, `bufsize`: x264 rate control settings
  - `audio_bitrate`: per-variant audio bitrate

Global transcode fields:
- `video_codec`, `video_preset`, `video_tune`, `video_fps`
- `audio_codec`, `audio_sample_rate`

## Test

Run unit tests:

```bash
go test ./...
```

Run with Docker:

```bash
docker compose up -d --build
```
