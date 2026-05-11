# Stage 2 - Dynamic Multi-Stream RTMP to ABR HLS

This stage replaces the single fixed RTMP endpoint with dynamic ingest paths and per-stream ABR output.

## Ingest

- RTMP publish URL format: `rtmp://<host>:1935/{app}/{streamKey}`
- Examples:
  - `rtmp://localhost:1935/live/stream1`
  - `rtmp://localhost:1935/live/stream2`

## Output Layout

Each stream gets an isolated output tree:

- `<storage_path>/<app>/<streamKey>/master.m3u8`
- `<storage_path>/<app>/<streamKey>/<variant>/index.m3u8`
- `<storage_path>/<app>/<streamKey>/<variant>/segment_*.ts`

Playback examples:

- `http://localhost:8080/hls/live/stream1/master.m3u8`
- `http://localhost:8080/hls/live/stream2/master.m3u8`
- `http://localhost:8080/hls/live/stream1/360p/index.m3u8`

## Runtime Controls

- `max_streams`: max concurrently active streams.
- `idle_timeout_sec`: keep worker alive for short reconnect windows before stopping.

## Storage Mode

- `storage_mode: disk` with `storage_path: /data/hls` for persistent output.
- `storage_mode: memory` with `storage_path: /dev/shm/hls` for memory-backed output.

Compose is preconfigured so you can switch by config only.

## DTS / Timestamp Notes

Reconnects can cause timestamp discontinuities. The pipeline applies FFmpeg timestamp normalization flags:

- `-fflags +genpts+igndts`
- `-use_wallclock_as_timestamps 1`
- `-vsync cfr`
- `-af aresample=async=1:first_pts=0`
- `-avoid_negative_ts make_zero`
- `-max_interleave_delta 0`

If needed, reduce `idle_timeout_sec` (for example `3`) to force fast worker rotation after disconnect.
