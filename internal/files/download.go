package files

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DownloadRequest downloads URL to Path: a file path, or a folder in which the
// file is named after the response. A Path ending in "/" is a folder, created if missing.
type DownloadRequest struct {
	URL       string `json:"url"`
	Path      string `json:"path"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// DownloadResult describes a finished download.
type DownloadResult struct {
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type,omitempty"`
	Resumed     bool   `json:"resumed,omitempty"`
}

// Download fetches a URL into a file, resuming from "<file>.part" when a previous
// attempt stopped. progress, if not nil, receives bytes written and the total (-1 if unknown).
func (o Ops) Download(ctx context.Context, req DownloadRequest, progress func(n, total int64)) (DownloadResult, error) {
	if progress == nil {
		progress = func(int64, int64) {}
	}
	target := req.Path
	if strings.HasSuffix(req.Path, "/") {
		if err := os.MkdirAll(req.Path, 0o755); err != nil {
			return DownloadResult{}, err
		}
	}
	var first *http.Response
	if fi, err := os.Stat(req.Path); err == nil && fi.IsDir() {
		resp, err := get(ctx, req.URL, 0)
		if err != nil {
			return DownloadResult{}, err
		}
		target = filepath.Join(req.Path, fileName(resp))
		first = resp
	}
	if _, err := os.Lstat(target); err == nil && !req.Overwrite {
		closeBody(first)
		return DownloadResult{}, fmt.Errorf("download to %s: %w", target, ErrExists)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		closeBody(first)
		return DownloadResult{}, err
	}
	part := target + ".part"
	var offset int64
	if fi, err := os.Stat(part); err == nil && fi.Mode().IsRegular() {
		offset = fi.Size()
	}
	resp := first
	if resp == nil || (offset > 0 && resp.Header.Get("Accept-Ranges") == "bytes") {
		closeBody(resp)
		var err error
		if resp, err = get(ctx, req.URL, offset); err != nil {
			return DownloadResult{}, err
		}
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	resumed := resp.StatusCode == http.StatusPartialContent && contentRangeStart(resp) == offset && offset > 0
	if resumed {
		flags = os.O_WRONLY | os.O_APPEND
	} else {
		offset = 0
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return DownloadResult{}, err
	}
	total := int64(-1)
	if resp.ContentLength >= 0 {
		total = offset + resp.ContentLength
	}
	w := &progressWriter{w: f, n: offset, total: total, report: progress}
	_, err = io.Copy(w, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download %s: %w (partial download kept for resuming)", req.URL, err)
	}
	progress(w.n, total)
	if total >= 0 && w.n != total {
		return DownloadResult{}, fmt.Errorf("download %s: got %d of %d bytes (partial download kept for resuming)", req.URL, w.n, total)
	}
	if err := os.Rename(part, target); err != nil {
		return DownloadResult{}, err
	}
	return DownloadResult{Path: target, Bytes: w.n, ContentType: resp.Header.Get("Content-Type"), Resumed: resumed}, nil
}

func get(ctx context.Context, rawURL string, offset int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "AgenticOS/1")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		resp.Body.Close()
		return get(ctx, rawURL, 0)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("download %s: %s", rawURL, resp.Status)
	}
	return resp, nil
}

func closeBody(resp *http.Response) {
	if resp != nil {
		resp.Body.Close()
	}
}

// fileName names a download after Content-Disposition, else the URL path.
func fileName(resp *http.Response) string {
	if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
		if name := safeName(params["filename"]); name != "" {
			return name
		}
	}
	if p, err := url.PathUnescape(resp.Request.URL.Path); err == nil {
		if name := safeName(path.Base(p)); name != "" {
			return name
		}
	}
	return "download"
}

func safeName(name string) string {
	name = strings.TrimSpace(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if name == "." || name == ".." || name == "/" || strings.ContainsRune(name, 0) {
		return ""
	}
	return name
}

func contentRangeStart(resp *http.Response) int64 {
	// "bytes 30000-99999/100000"
	cr := strings.TrimPrefix(resp.Header.Get("Content-Range"), "bytes ")
	start, _, ok := strings.Cut(cr, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(start, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

type progressWriter struct {
	w      io.Writer
	n      int64
	total  int64
	last   time.Time
	report func(n, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if now := time.Now(); now.Sub(p.last) > 200*time.Millisecond {
		p.last = now
		p.report(p.n, p.total)
	}
	return n, err
}
