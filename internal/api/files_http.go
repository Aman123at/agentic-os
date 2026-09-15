package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/amantiwari/agentic-os/internal/files"
)

// The plain HTTP routes for file bytes (PLAN.md §13). Browsers cannot stream
// over Connect, so Preview's media, downloads and uploads use these, and every
// byte still moves through a worker running as the aos user.

// inlineTypes are the types a browser shows without running anything, so they
// may open in place. Everything else, HTML and SVG included, downloads.
var inlineTypes = map[string]string{
	".txt": "text/plain; charset=utf-8", ".log": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8",
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon",
	".pdf": "application/pdf",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg", ".mov": "video/quicktime",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav", ".ogg": "audio/ogg",
	".oga": "audio/ogg", ".opus": "audio/ogg", ".flac": "audio/flac",
}

// rawType is the Content-Type for path and whether it may open in place.
func rawType(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if t, ok := inlineTypes[ext]; ok {
		return t, true
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t, false
	}
	return "application/octet-stream", false
}

// fileStatus is the HTTP status for a file operation's error.
func fileStatus(err error) int {
	var tooBig *http.MaxBytesError
	switch {
	case errors.Is(err, files.ErrNotExist):
		return http.StatusNotFound
	case errors.Is(err, files.ErrPermission):
		return http.StatusForbidden
	case errors.Is(err, files.ErrExists):
		return http.StatusConflict
	case errors.Is(err, files.ErrNotFile):
		return http.StatusBadRequest
	case errors.As(err, &tooBig):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// rawFile serves GET /files/raw?path=[&download=1]: a file's bytes, or the one
// range a Range header asks for. Only inlineTypes open in place, and every
// response carries a sandbox CSP and nosniff, so a file never runs as part of
// the Desktop.
func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	st, ok := s.UserFiles.(files.Streamer)
	if !ok {
		http.Error(w, "file streams are not available", http.StatusNotImplemented)
		return
	}
	p, err := (fileService{s}).abs(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "the path is empty", http.StatusBadRequest)
		return
	}
	started := false
	err = st.ReadStream(r.Context(), files.StreamArgs{Path: p, Range: r.Header.Get("Range")}, func(h files.StreamHeader) error {
		started = true
		hdr := w.Header()
		ctype, inline := rawType(p)
		disposition := "inline"
		if !inline || r.URL.Query().Get("download") != "" {
			disposition = "attachment"
		}
		if v := mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(p)}); v != "" {
			disposition = v
		}
		hdr.Set("Content-Type", ctype)
		hdr.Set("Content-Disposition", disposition)
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Content-Security-Policy", "sandbox")
		hdr.Set("Cache-Control", "private, no-cache")
		hdr.Set("Accept-Ranges", "bytes")
		hdr.Set("Last-Modified", h.ModTime.UTC().Format(http.TimeFormat))
		switch {
		case h.Unsatisfiable:
			hdr.Set("Content-Range", fmt.Sprintf("bytes */%d", h.Size))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		case h.Partial:
			hdr.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", h.Start, h.Start+h.Length-1, h.Size))
			hdr.Set("Content-Length", strconv.FormatInt(h.Length, 10))
			w.WriteHeader(http.StatusPartialContent)
		default:
			hdr.Set("Content-Length", strconv.FormatInt(h.Length, 10))
			w.WriteHeader(http.StatusOK)
		}
		return nil
	}, w)
	// After the headers, an error means the browser went away or the file
	// changed; the short body is all that can tell it.
	if err != nil && !started {
		http.Error(w, err.Error(), fileStatus(err))
	}
}

// upload serves POST /upload?path=[&overwrite=1]: the request body becomes the
// file, written as the aos user through a temporary file, so a failed or
// cut-short upload leaves nothing behind. It is audited like a write.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	st, ok := s.UserFiles.(files.Streamer)
	if !ok {
		http.Error(w, "file streams are not available", http.StatusNotImplemented)
		return
	}
	p, err := (fileService{s}).abs(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, "the path is empty", http.StatusBadRequest)
		return
	}
	switch {
	case r.ContentLength < 0:
		http.Error(w, "an upload needs its Content-Length", http.StatusLengthRequired)
		return
	case r.ContentLength > files.MaxUpload:
		http.Error(w, fmt.Sprintf("an upload can be at most %d GB", files.MaxUpload>>30), http.StatusRequestEntityTooLarge)
		return
	}
	args := files.WriteStreamArgs{Path: p, Overwrite: r.URL.Query().Get("overwrite") != "", Size: r.ContentLength}
	n, err := st.WriteStream(r.Context(), args, http.MaxBytesReader(w, r.Body, files.MaxUpload))
	(fileService{s}).audit(r.Context(), "upload", p, err)
	if err != nil {
		http.Error(w, err.Error(), fileStatus(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"path": p, "size": n})
}
