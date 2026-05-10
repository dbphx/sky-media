# Media Engine (RTMP/STMP -> HLS) - Design Notes

## 1) Muc tieu

Xay dung mot media-engine giong huong MediaMTX, nhung pham vi toi gian:
- Chi nhan ingest tu RTMP hoac STMP (neu STMP la giao thuc ingest rieng cua he thong).
- Convert thanh HLS de playback qua HTTP/CDN.
- Uu tien performance cao, do tre thap, de scale ngang.

Tai lieu nay tong hop kien truc, quyet dinh ky thuat, thong so van hanh va lo trinh trien khai.

## 2) Scope

### Trong scope
- Ingest RTMP/STMP.
- Phat hien codec dau vao.
- Fast path transmux (khong re-encode) neu input da phu hop HLS.
- Fallback transcode sang H.264/AAC khi can.
- Xuat HLS (playlist + segments).
- HTTP serving endpoint cho player/CDN.
- Metrics va health check co ban.

### Ngoai scope (giai doan dau)
- WebRTC ingest/playback.
- DRM/CENC.
- Multi-tenant billing phuc tap.
- Active-active multi-region.

## 3) Kien truc tong the

```text
Publisher (OBS/Encoder)
   -> Ingest Service (RTMP/STMP)
      -> Stream Worker (per stream)
         -> [Fast path] Transmux -> HLS Muxer
         -> [Fallback]  Transcode -> HLS Muxer
            -> Segment Storage (local NVMe/tmpfs)
               -> HTTP HLS Service (/hls/{app}/{stream}/index.m3u8)
                  -> CDN/Player
```

### 3.1 Ingest Service
- Lang nghe RTMP/STMP, authenticate stream key (optional webhook).
- Chuan hoa timestamp, xu ly reconnect publisher.
- Day packet vao queue cho stream worker.

### 3.2 Stream Worker (1 worker / 1 stream)
- Isolate tai nguyen theo stream, tranh anh huong cheo.
- Kien truc queue SPSC (single producer/single consumer) de giam lock.
- Tu dong chon fast path hoac fallback path.

### 3.3 Fast path (khuyen nghi mac dinh)
- Dieu kien: video H.264 + audio AAC hop le cho HLS.
- Hanh vi: khong encode lai, chi transmux sang HLS.
- Loi ich: tiet kiem CPU rat lon, tang so stream/node.

### 3.4 Fallback transcode
- Khi input codec khong phu hop HLS (VD: HEVC + MP3 khong mong muon).
- Dung FFmpeg worker (CPU hoac GPU) de dua ve H.264/AAC.
- Goi y: bat/tat bang config `allowTranscodeFallback`.

### 3.5 HLS Muxer + HTTP Serving
- Tao `master.m3u8` (neu ABR) va `index.m3u8` cho tung variant.
- Segment co the la MPEG-TS (`.ts`) hoac fMP4 (`.m4s`).
- Ghi segment theo pattern append -> atomic rename de playlist nhat quan.
- Serve endpoint: `/hls/{app}/{stream}/index.m3u8`.

## 4) Lua chon dinh dang HLS

### 4.1 MPEG-TS
- Tuong thich rong, de trien khai.
- Do tre thuong cao hon mot chut so voi fMP4/LL-HLS.

### 4.2 fMP4 (CMAF)
- Hieu qua tot hon cho low latency va CDN cach moi.
- Nen uu tien neu dinh huong LL-HLS.

Khuyen nghi MVP:
- Bat dau voi MPEG-TS cho don gian.
- Nang cap fMP4 khi can giam latency/toi uu cache.

## 5) Thong so ky thuat de dat performance cao

- Segment duration: `1-2s`.
- Playlist window: `6-10` segments.
- Bat `independent_segments`.
- GOP co dinh: `gop = fps * segment_duration` (VD 25fps, seg 2s => GOP 50).
- Audio AAC LC 48kHz.
- Co che ring buffer per-stream, tranh global lock.
- Tach ingest I/O va muxing/transcoding thread.
- Su dung local NVMe/tmpfs cho segment tam.
- Cleanup segment cu theo sliding window de tranh leak disk.

## 6) API/Config toi thieu

### 6.1 Endpoint goi y
- `POST /v1/streams/{app}/{name}/publish` (auth hook optional).
- `GET /hls/{app}/{name}/index.m3u8`.
- `GET /healthz`.
- `GET /metrics` (Prometheus).

### 6.2 Cau hinh
- `rtmpListen`, `stmpListen`
- `hlsSegmentDuration`
- `hlsSegmentCount`
- `hlsSegmentType` (`ts` | `fmp4`)
- `allowTranscodeFallback`
- `storagePath`
- `record` (on/off)
- `authWebhook` (optional)

## 7) Transcode profile goi y (fallback)

- Video: H.264, keyframe interval khop segment.
- Audio: AAC 128k, 48kHz.
- Preset uu tien throughput (CPU/GPU tuy ha tang).

FFmpeg baseline (tham khao):

```bash
ffmpeg -i rtmp://ingest/live/stream \
  -c:v h264_nvenc -preset p4 -tune ll \
  -g 48 -keyint_min 48 -sc_threshold 0 \
  -c:a aac -b:a 128k -ar 48000 \
  -f hls \
  -hls_time 2 \
  -hls_playlist_type event \
  -hls_flags independent_segments+delete_segments+append_list \
  -hls_segment_type fmp4 \
  -master_pl_name master.m3u8 \
  -var_stream_map "v:0,a:0" \
  out_%v.m3u8
```

Luu y:
- Day la baseline minh hoa; can tune theo CPU/GPU, bitrate ladder va muc tieu latency.

## 8) Kha nang mo rong (scaling)

- Scale ngang theo stream count: 1 stream = 1 worker.
- Admission control: tu choi publish khi het tai nguyen.
- Sticky routing stream theo stream key (neu co nhieu ingest node).
- Tach ingest layer va transcode layer khi tai lon.
- Dua HLS qua CDN de giam tai origin.

## 9) Observability va van hanh

Bat buoc co:
- Metrics: stream active, ingest bitrate, dropped frames, transcode cpu/gpu, segment create latency, hls 4xx/5xx.
- Structured logs theo stream_id.
- Alert:
  - stream down dot ngot
  - segment khong sinh > N giay
  - CPU/GPU > nguong trong thoi gian dai

## 10) Lo trinh trien khai

### Phase 1 (MVP)
- RTMP ingest.
- HLS transmux cho input H.264/AAC.
- HTTP playback endpoint.
- Metrics + healthcheck co ban.

### Phase 2
- STMP ingest.
- Fallback transcode FFmpeg worker.
- Auth webhook + stream key policy.

### Phase 3
- fMP4/LL-HLS.
- Autoscaling worker pool.
- Multi-node + CDN toi uu.

## 11) Rủi ro va cach giam

- Input codec khong dong nhat -> can fallback transcode ro rang.
- GOP khong khop segment -> giat hinh, playlist drift -> ep keyframe theo segment.
- I/O disk choke -> dung NVMe/tmpfs + cleanup thong minh.
- Cong suat vuot nguong -> admission control + autoscale.

## 12) Tieu chi nghiem thu de benchmark

- So stream dong thoi toi da tren 1 node (fast path/fallback tach rieng).
- CPU/GPU trung binh tai 70-80% load.
- P95 segment generation latency.
- Playback startup time (player join).
- Ti le rebuffering va loi HLS 4xx/5xx.

---

Neu can, tai lieu tiep theo nen bo sung:
1) Sequence diagram publish -> segment -> playback.
2) BANG sizing (stream/node) theo profile bitrate.
3) Bo test benchmark (100/500/1000 stream) va script test tu dong.
