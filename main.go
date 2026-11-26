package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kkdai/youtube/v2"
)

var yt = youtube.Client{}

type result struct{ ID, Title, Channel, Length string }

type view struct {
	Q, Video, Title, Meta, Desc, Err string
	Results                          []result
}

var page = template.Must(template.New("p").Parse(`<!doctype html><meta charset=utf-8>
<meta name=viewport content="width=device-width,initial-scale=1">
<title>nutz</title>
<style>body{font-family:sans-serif;max-width:900px;margin:2em auto;padding:0 1em;background:#111;color:#eee}
a{color:#6cf}input{padding:.5em;width:70%}video{width:100%;background:#000}li{margin:.6em 0}
.desc{white-space:pre-wrap;word-break:break-word;background:#1b1b1b;padding:1em;border-radius:6px;margin-top:.5em}
summary{cursor:pointer}</style>
<h2><a href="/">nutz</a></h2>
<form action=/search><input name=q value="{{.Q}}" placeholder=Search autofocus> <button>Go</button></form>
{{if .Video}}
<h3>{{.Title}}</h3>
<video controls autoplay src="/stream?id={{.Video}}"></video>
<p><small>{{.Meta}}</small></p>
{{if .Desc}}<details open><summary>Description</summary><div class=desc>{{.Desc}}</div></details>{{end}}
{{end}}
{{if .Err}}<p style=color:#f66>{{.Err}}</p>{{end}}
<ul>{{range .Results}}<li><a href="/watch?v={{.ID}}">{{.Title}}</a><br><small>{{.Channel}} · {{.Length}}</small></li>{{end}}</ul>`))

// --- video cache ---
type cached struct {
	v *youtube.Video
	t time.Time
}

var (
	mu    sync.Mutex
	cache = map[string]cached{}
)

func getVideo(id string) (*youtube.Video, error) {
	mu.Lock()
	c, ok := cache[id]
	mu.Unlock()
	if ok && time.Since(c.t) < 30*time.Minute {
		return c.v, nil
	}
	v, err := yt.GetVideo(id)
	if err != nil {
		return nil, err
	}
	mu.Lock()
	cache[id] = cached{v, time.Now()}
	mu.Unlock()
	return v, nil
}

// bestMuxed returns the highest-resolution MP4 that has video and audio together.
func bestMuxed(vid *youtube.Video) *youtube.Format {
	var best *youtube.Format
	for i := range vid.Formats {
		f := &vid.Formats[i]
		if strings.HasPrefix(f.MimeType, "video/mp4") && f.AudioChannels > 0 {
			if best == nil || f.Height > best.Height {
				best = f
			}
		}
	}
	return best
}

// --- search ---
func search(q string) ([]result, error) {
	body, _ := json.Marshal(map[string]any{
		"query": q,
		"context": map[string]any{"client": map[string]any{
			"clientName": "WEB", "clientVersion": "2.20240101.00.00", "hl": "en"}},
	})
	resp, err := http.Post("https://www.youtube.com/youtubei/v1/search?prettyPrint=false",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	var out []result
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if vr, ok := v["videoRenderer"].(map[string]any); ok {
				id, _ := vr["videoId"].(string)
				out = append(out, result{id, text(vr["title"]), text(vr["ownerText"]), text(vr["lengthText"])})
				return
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(data)
	return out, nil
}

func text(n any) string {
	m, _ := n.(map[string]any)
	if s, ok := m["simpleText"].(string); ok {
		return s
	}
	if runs, ok := m["runs"].([]any); ok && len(runs) > 0 {
		r, _ := runs[0].(map[string]any)
		s, _ := r["text"].(string)
		return s
	}
	return ""
}

// serveFormat proxies a format with HTTP Range support, fetching from
// YouTube in <10 MB chunks.
func serveFormat(w http.ResponseWriter, r *http.Request, vid *youtube.Video, f *youtube.Format) {
	u, err := yt.GetStreamURL(vid, f)
	if err != nil {
		log.Println("stream url:", err)
		http.Error(w, err.Error(), 502)
		return
	}
	total := f.ContentLength
	if total <= 0 {
		http.Error(w, "unknown content length", 502)
		return
	}
	start, end := int64(0), total-1
	partial := false
	if rh := r.Header.Get("Range"); strings.HasPrefix(rh, "bytes=") {
		parts := strings.SplitN(strings.TrimPrefix(rh, "bytes="), "-", 2)
		if s, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
			start = s
		}
		if len(parts) == 2 && parts[1] != "" {
			if e, err := strconv.ParseInt(parts[1], 10, 64); err == nil && e < total {
				end = e
			}
		}
		partial = true
	}
	if start < 0 || start >= total || start > end {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
		http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h := w.Header()
	h.Set("Content-Type", strings.Split(f.MimeType, ";")[0])
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
		w.WriteHeader(http.StatusPartialContent)
	}
	const chunk int64 = 9 << 20
	for pos := start; pos <= end; {
		ce := pos + chunk - 1
		if ce > end {
			ce = end
		}
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s&range=%d-%d", u, pos, ce), nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Println("upstream:", err)
			return
		}
		if resp.StatusCode >= 400 {
			log.Println("upstream status:", resp.Status)
			resp.Body.Close()
			return
		}
		n, err := io.Copy(w, resp.Body)
		resp.Body.Close()
		if err != nil || n == 0 {
			return
		}
		pos += n
	}
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		page.Execute(w, view{})
	})
	http.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		res, err := search(q)
		v := view{Q: q, Results: res}
		if err != nil {
			v.Err = err.Error()
		}
		page.Execute(w, v)
	})
	http.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("v")
		v := view{Video: id}
		vid, err := getVideo(id)
		if err != nil {
			v.Err = err.Error()
		} else {
			v.Title, v.Desc = vid.Title, vid.Description
			meta := []string{vid.Author, fmt.Sprintf("%d views", vid.Views), vid.Duration.Round(time.Second).String()}
			if !vid.PublishDate.IsZero() {
				meta = append(meta, vid.PublishDate.Format("2 Jan 2006"))
			}
			v.Meta = strings.Join(meta, " · ")
		}
		page.Execute(w, v)
	})
	http.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		vid, err := getVideo(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		f := bestMuxed(vid)
		if f == nil {
			http.Error(w, "no playable format", 404)
			return
		}
		serveFormat(w, r, vid, f)
	})
	fmt.Println("nutz is running on http://localhost:3000")
	log.Fatal(http.ListenAndServe("127.0.0.1:3000", nil))
}
