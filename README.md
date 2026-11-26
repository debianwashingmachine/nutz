# nutz

A minimal, portable, YouTube frontend written in Go.

## Features

- Search
- Watch page with description, views, duration, and publish date
- Streaming proxy with seeking (HTTP Range support)

## Notes

- Uses [kkdai/youtube](https://github.com/kkdai/youtube) for stream extraction.
- Listens on `127.0.0.1` only, so it is not exposed to your network.
- For personal use. Respect YouTube's Terms of Service.

## Problems

- Some videos may not work.
- No quality selection (some problems with streaming).
- Videos are only 360p.
