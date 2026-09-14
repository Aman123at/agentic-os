package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	readability "github.com/go-shiori/go-readability"

	"github.com/amantiwari/agentic-os/internal/files"
	"github.com/amantiwari/agentic-os/internal/policy"
)

// InternetTools returns the Internet group.
func InternetTools() []Tool {
	return []Tool{download{}, httpRequest{}, webSearch{}}
}

// ---------------------------------------------------------------- download

type download struct{}

func (download) Spec() Spec {
	return Spec{Name: "download", Description: "Download a URL to a file (default: into ~/Downloads, named after the response). Resumes an interrupted download of the same file. Shows progress to the user.",
		Parameters: object(map[string]any{
			"url":       str("http or https URL"),
			"path":      optStr("File path, or an existing folder to save into (default ~/Downloads)"),
			"overwrite": optBool("Replace an existing file"),
		})}
}

func (download) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		URL, Path string
		Overwrite bool
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("not an http(s) URL: %q", a.URL)
	}
	dest := filepath.Join(env.Home, "Downloads")
	if a.Path != "" {
		dest = env.Abs(a.Path)
	}
	c := &Call{Summary: fmt.Sprintf("Download %s to %s", oneLine(a.URL, 120), env.Display(dest)),
		Policy: policy.Call{Tool: "download", Folder: dest, Effects: []policy.Effect{{Path: dest, Op: policy.Write}}}}
	if exists, dir := env.stat(dest); exists && !dir {
		c.Policy.Folder = filepath.Dir(dest)
		if a.Overwrite {
			c.Policy.Risky = true
			c.Policy.Reasons = []string{"overwrites " + env.Display(dest)}
		}
	}
	target := dest
	if a.Path == "" || strings.HasSuffix(a.Path, "/") {
		target += "/" // a folder, created if missing
	}
	c.Run = func(ctx context.Context, r Run) Result {
		var res files.DownloadResult
		progress := func(n, total int64) {
			if r.Progress != nil {
				r.Progress(a.URL, dest, n, total)
			}
		}
		err := env.Files(r.Widen).Run(ctx, files.OpDownload, files.DownloadRequest{URL: a.URL, Path: target, Overwrite: a.Overwrite}, &res, progress)
		if err != nil {
			return fileError(env, err)
		}
		note := ""
		if res.Resumed {
			note = " (resumed)"
		}
		return Result{Output: fmt.Sprintf("Downloaded %s to %s: %s, %s%s.", a.URL, env.Display(res.Path), humanSize(res.Bytes), orUnknown(res.ContentType), note)}
	}
	return c, nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown type"
	}
	return s
}

// ---------------------------------------------------------------- http_request

const (
	maxBody       = 10 << 20
	maxPageResult = 20 << 10
)

type httpRequest struct{}

func (httpRequest) Spec() Spec {
	return Spec{Name: "http_request", Description: "Make an HTTP request and read the response. HTML pages are converted to readable Markdown. Sending a body (an upload) is a Risky Action. Use download for files.",
		Parameters: object(map[string]any{
			"method":  optStr("GET (default), HEAD, POST, PUT, PATCH or DELETE"),
			"url":     str("http or https URL"),
			"headers": map[string]any{"type": []string{"object", "null"}, "additionalProperties": map[string]any{"type": "string"}, "description": "Request headers"},
			"body":    optStr("Request body"),
		})}
}

func (httpRequest) Prepare(_ context.Context, env *Env, args json.RawMessage) (*Call, error) {
	var a struct {
		Method, URL, Body string
		Headers           map[string]string
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	a.Method = strings.ToUpper(a.Method)
	if a.Method == "" {
		a.Method = http.MethodGet
	}
	u, err := url.Parse(a.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("not an http(s) URL: %q", a.URL)
	}
	c := &Call{Summary: fmt.Sprintf("%s %s", a.Method, oneLine(a.URL, 160)), Policy: policy.Call{Tool: "http_request", Folder: u.Host},
		ReadOnly: a.Method == http.MethodGet || a.Method == http.MethodHead}
	if a.Body != "" {
		c.Policy.Risky = true
		c.Policy.Reasons = []string{fmt.Sprintf("uploads %s to %s", humanSize(int64(len(a.Body))), u.Host)}
	}
	c.Run = func(ctx context.Context, r Run) Result {
		req, err := http.NewRequestWithContext(ctx, a.Method, a.URL, strings.NewReader(a.Body))
		if err != nil {
			return Errorf("%v", err)
		}
		req.Header.Set("User-Agent", "AgenticOS/1")
		for k, v := range a.Headers {
			req.Header.Set(k, v)
		}
		client := env.HTTP
		if client == nil {
			client = http.DefaultClient
		}
		resp, err := client.Do(req)
		if err != nil {
			return Errorf("%v", err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
		if err != nil {
			return Errorf("reading the response: %v", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s\n", resp.Proto, resp.Status)
		for _, h := range []string{"Content-Type", "Content-Length", "Location", "Last-Modified"} {
			if v := resp.Header.Get(h); v != "" {
				fmt.Fprintf(&b, "%s: %s\n", h, v)
			}
		}
		b.WriteString("\n")
		text := responseText(resp, body)
		if len(body) > maxBody {
			text += "\n… body larger than 10 MB, cut off (use download for large files)"
		}
		var ref string
		if len(text) > maxPageResult && env.Outputs != nil {
			if name, f, err := env.Outputs.Create(env.TaskID, r.StepID+".out"); err == nil {
				_, _ = f.WriteString(text)
				_ = f.Close()
				ref = name
			}
			cut := validUTF8Prefix([]byte(text[:maxPageResult]))
			text = fmt.Sprintf("%s\n… [%d more bytes; read_output ref=%q offset=%d continues]", cut, len(text)-len(cut), ref, len(cut))
		}
		b.WriteString(text)
		return Result{Output: b.String(), OutputRef: ref, Error: resp.StatusCode >= 400}
	}
	return c, nil
}

// responseText renders a response body for the model.
func responseText(resp *http.Response, body []byte) string {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		if article, err := readability.FromReader(strings.NewReader(string(body)), resp.Request.URL); err == nil && strings.TrimSpace(article.Content) != "" {
			if md, err := htmltomarkdown.ConvertString(article.Content); err == nil {
				title := ""
				if article.Title != "" {
					title = "# " + article.Title + "\n\n"
				}
				return title + md
			}
		}
		if md, err := htmltomarkdown.ConvertString(string(body)); err == nil {
			return md
		}
		return string(body)
	case utf8.Valid(body):
		return string(body)
	}
	return fmt.Sprintf("(binary body, %s, %s; use download to save it)", humanSize(int64(len(body))), orUnknown(mediaType))
}

// ---------------------------------------------------------------- web_search

type webSearch struct{}

func (webSearch) Spec() Spec {
	return Spec{Name: "web_search", Hosted: "web_search"}
}

func (webSearch) Prepare(context.Context, *Env, json.RawMessage) (*Call, error) {
	return nil, errors.New("web_search is run by the model provider")
}
