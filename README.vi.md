# Sky Media Engine

Sky Media Engine là một dịch vụ media viết bằng Go, dùng để nhận livestream và xuất HLS độ trễ thấp. Dự án được giữ trong phạm vi gọn, dễ vận hành:

- nhận RTMP push tại `/{app}/{streamKey}`
- tùy chọn kéo RTSP thông qua HTTP API
- dùng FFmpeg để transcode thành các biến thể ABR HLS
- phục vụ file HLS qua HTTP
- lưu segment HLS bằng memory hoặc disk

Tài liệu thiết kế:

- [Thiết kế RTMP sang HLS](docs/media-engine-rtmp-to-hls.md)
- [Ghi chú Stage 1 ABR](docs/stage1-abr.md)
- [Ghi chú Stage 2 multi-stream](docs/stage2-multistream.md)

## Tính năng

- Đọc cấu hình runtime từ YAML.
- Nhận RTMP publisher tại `rtmp://<host>:1935/{app}/{streamKey}`.
- Chạy một pipeline FFmpeg cho mỗi stream đang hoạt động.
- Tạo master playlist HLS và các playlist variant theo cấu hình.
- Phục vụ HLS từ thư mục storage đã cấu hình.
- Cung cấp `GET /healthz`.
- Có thể bật API kéo RTSP:
  - `POST /api/ingest/rtsp`
  - `DELETE /api/ingest/rtsp/{app}/{stream}`

## Cấu trúc dự án

- `cmd/engine/main.go`: entrypoint của service.
- `internal/config/config.go`: model cấu hình, giá trị mặc định, load và validate.
- `internal/engine/engine.go`: nối RTMP ingest, RTSP ingest, transcoding và HLS server.
- `internal/ingest/rtmp`: RTMP ingest server.
- `internal/ingest/rtsp`: API kéo RTSP tùy chọn.
- `internal/transcode`: FFmpeg pipeline và stream manager.
- `internal/serve/hls`: HTTP server phục vụ HLS và health check.
- `config/config.yaml`: cấu hình runtime mặc định.
- `docs/`: ghi chú thiết kế và triển khai.

## Yêu cầu

- Go 1.25 trở lên
- FFmpeg có trong `PATH`
- Docker và Docker Compose nếu chạy bằng container

## Cấu hình

Cấu hình mặc định nằm tại [config/config.yaml](config/config.yaml):

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

Tùy chọn storage:

- `storage_mode: "memory"` lưu HLS output trên đường dẫn dùng RAM, ví dụ `/dev/shm/hls`.
- `storage_mode: "disk"` lưu HLS output trên đường dẫn bền vững, ví dụ `/data/hls`.
- Docker Compose đã mount sẵn cả `./data:/data` và tmpfs `/dev/shm/hls`, nên chỉ cần đổi `storage_mode` và `storage_path` trong config.

## Chạy local

```bash
go run ./cmd/engine -config ./config/config.yaml
```

Service sẽ mở:

- RTMP ingest tại `localhost:1935`
- HTTP HLS và health endpoint tại `localhost:8080`

## Chạy bằng Docker Compose

```bash
docker compose up --build -d
docker compose logs -f media-engine
```

Dừng service:

```bash
docker compose down
```

## Nhận RTMP

Push stream bằng FFmpeg:

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream1
```

Push stream thứ hai:

```bash
ffmpeg -re -stream_loop -1 -i input2.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream2
```

OBS có thể publish với thông tin:

- Server: `rtmp://localhost:1935/live`
- Stream key: `stream1`

## Kéo RTSP

RTSP ingest mặc định đang tắt. Bật trong config:

```yaml
rtsp_ingest_api: true
```

Bắt đầu kéo một nguồn RTSP:

```bash
curl -X POST http://localhost:8080/api/ingest/rtsp \
  -H 'Content-Type: application/json' \
  -d '{"app":"camera","stream":"cam1","url":"rtsp://user:pass@camera-host/stream"}'
```

Dừng kéo RTSP:

```bash
curl -X DELETE http://localhost:8080/api/ingest/rtsp/camera/cam1
```

## Xem HLS

HLS được phục vụ từ HTTP root theo cùng path `{app}/{streamKey}` như lúc ingest.

Ví dụ:

- Master playlist: `http://localhost:8080/live/stream1/master.m3u8`
- Playlist 360p: `http://localhost:8080/live/stream1/360p/index.m3u8`
- Playlist 720p: `http://localhost:8080/live/stream1/720p/index.m3u8`
- Ví dụ RTSP pull: `http://localhost:8080/camera/cam1/master.m3u8`

Health check:

```bash
curl http://localhost:8080/healthz
```

## Test

Chạy unit test:

```bash
go test ./...
```

Chạy service local:

```bash
go run ./cmd/engine -config ./config/config.yaml
```

Mở terminal khác và kiểm tra health endpoint:

```bash
curl http://localhost:8080/healthz
```

Kết quả mong muốn:

```text
ok
```

Push một file mẫu qua RTMP:

```bash
ffmpeg -re -stream_loop -1 -i input.mp4 \
  -c:v libx264 -c:a aac -f flv \
  rtmp://localhost:1935/live/stream1
```

Chờ vài giây để service tạo segment HLS, sau đó kiểm tra master playlist:

```bash
curl http://localhost:8080/live/stream1/master.m3u8
```

Response nên có nội dung playlist HLS như `#EXTM3U` và các variant playlist. Bạn cũng có thể mở URL này bằng VLC, Safari hoặc HLS player:

```text
http://localhost:8080/live/stream1/master.m3u8
```

Test bằng Docker Compose:

```bash
docker compose up --build -d
docker compose logs -f media-engine
```

Sau đó dùng cùng RTMP push URL và HLS playback URL ở trên.

## Ghi chú vận hành

- Để playback ổn định, đặt keyframe interval của publisher khớp với `segment_time`; mặc định là `2` giây.
- GOP size được tính từ `segment_time * video_fps`.
- Mỗi stream đang chạy ghi output vào `storage_path/{app}/{streamKey}`.
- Pipeline FFmpeg có các flag chuẩn hóa timestamp để giảm lỗi reconnect như non-monotonic DTS.
- `max_streams` giới hạn số pipeline chạy đồng thời.
- `idle_timeout_sec` điều khiển việc dọn các stream session không còn hoạt động.
