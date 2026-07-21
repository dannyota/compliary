package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSaveByFinalName_SingleRequest(t *testing.T) {
	var requests atomic.Int32
	finalName := "CIS_Controls_v8.1.pdf"
	body := []byte("%PDF-fake-content-here")

	// Redirect once so final-name resolution is exercised.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/final/"+finalName, http.StatusFound)
		case "/final/" + finalName:
			w.WriteHeader(http.StatusOK)
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	destDir := t.TempDir()
	var msgs []string
	report := func(s string) { msgs = append(msgs, s) }

	c := ts.Client()
	if err := saveByFinalName(c, ts.URL+"/start", destDir, pdfMagic, report); err != nil {
		t.Fatalf("saveByFinalName: %v", err)
	}

	// Redirect counts as 2 server-side requests (initial + redirect target),
	// but only 1 logical download — no second full GET must occur.
	if got := requests.Load(); got != 2 {
		t.Errorf("expected 2 server requests (redirect + final), got %d", got)
	}

	expected := kebabName(finalName)
	dest := filepath.Join(destDir, expected)
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != string(body) {
		t.Errorf("file content = %q, want %q", data, body)
	}

	if len(msgs) != 1 || msgs[0] != "downloaded: cis/"+expected {
		t.Errorf("report messages: %v", msgs)
	}
}

func TestSaveByFinalName_SkipExists(t *testing.T) {
	var requests atomic.Int32
	finalName := "existing-file.pdf"
	body := []byte("%PDF-content")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer ts.Close()

	destDir := t.TempDir()
	dest := filepath.Join(destDir, finalName)
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	var msgs []string
	report := func(s string) { msgs = append(msgs, s) }

	c := ts.Client()
	if err := saveByFinalName(c, ts.URL+"/"+finalName, destDir, pdfMagic, report); err != nil {
		t.Fatalf("saveByFinalName: %v", err)
	}

	// One request is still needed to discover the final filename, but the
	// file is not overwritten.
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old" {
		t.Errorf("existing file was overwritten: %q", data)
	}
	if len(msgs) != 1 || msgs[0] != "skip (exists): cis/"+finalName {
		t.Errorf("report messages: %v", msgs)
	}
}

func TestSaveByFinalName_BadMagic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>error page</html>"))
	}))
	defer ts.Close()

	destDir := t.TempDir()
	c := ts.Client()
	err := saveByFinalName(c, ts.URL+"/file.pdf", destDir, pdfMagic, func(string) {})
	if err == nil {
		t.Fatal("expected error for bad magic")
	}

	// No file should be left behind.
	entries, _ := os.ReadDir(destDir)
	if len(entries) != 0 {
		t.Errorf("destDir should be empty after bad magic, got %d entries", len(entries))
	}
}

func TestRetry_503ThenOK(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := ts.Client()
	resp, err := get(c, ts.URL+"/test")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 attempts (503 then 200), got %d", got)
	}
}

func TestRetry_NoRetryOn404(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.NotFound(w, r)
	}))
	defer ts.Close()

	c := ts.Client()
	_, err := get(c, ts.URL+"/missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}

	if got := attempts.Load(); got != 1 {
		t.Errorf("expected 1 attempt (no retry on 404), got %d", got)
	}
}

func TestRetry_ExhaustedOn503(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := ts.Client()
	_, err := get(c, ts.URL+"/fail")
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestRetry_429Retried(t *testing.T) {
	var attempts atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer ts.Close()

	c := ts.Client()
	resp, err := get(c, ts.URL+"/throttle")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 attempts (429 then 200), got %d", got)
	}
}
