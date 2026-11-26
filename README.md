# nutz

A minimal, portable, Invidious-style YouTube frontend written in Go. It builds to a single executable with no Docker or database.

## Features

- Search
- Watch page with description, channel, views, duration, and publish date
- Streaming proxy with seeking (HTTP Range support)

## Run

Requires [Go](https://go.dev/dl/) 1.21 or newer.

```
go mod tidy
go build -o nutz.exe
./nutz.exe
```

Then open http://localhost:3000.

## Notes

- Plays the best stream that has video and audio together (usually 360p).
- Uses [kkdai/youtube](https://github.com/kkdai/youtube) for stream extraction. If playback breaks, update it with `go get -u github.com/kkdai/youtube/v2`.
- Listens on `127.0.0.1` only, so it is not exposed to your network.
- For personal use. Respect YouTube's Terms of Service.
