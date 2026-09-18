package files

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadNamesTheFileAndResumesAPartialDownload(t *testing.T) {
	ops, home := machine(t)
	body := strings.Repeat("0123456789", 10_000)
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		w.Header().Set("Content-Disposition", `attachment; filename="data set.csv"`)
		http.ServeContent(w, r, "", time.Time{}, strings.NewReader(body))
	}))
	defer srv.Close()

	downloads := filepath.Join(home, "Downloads")
	// A previous attempt stopped after 30 000 bytes.
	writeFile(t, filepath.Join(downloads, "data set.csv.part"), body[:30_000])

	var last int64
	res, err := ops.Download(context.Background(), DownloadRequest{URL: srv.URL + "/export?id=7", Path: downloads}, func(n, total int64) { last = n })
	if err != nil {
		t.Fatal(err)
	}
	if res.Path != filepath.Join(downloads, "data set.csv") || res.Bytes != int64(len(body)) || !res.Resumed {
		t.Errorf("result %+v", res)
	}
	if readFile(t, res.Path) != body {
		t.Error("downloaded content differs")
	}
	if len(ranges) == 0 || ranges[len(ranges)-1] != "bytes=30000-" || last != int64(len(body)) {
		t.Errorf("ranges %q, last progress %d", ranges, last)
	}

	if _, err := ops.Download(context.Background(), DownloadRequest{URL: srv.URL, Path: res.Path}, nil); !errors.Is(err, ErrExists) {
		t.Errorf("download onto an existing file: %v, want ErrExists", err)
	}
}
