package packbin

// The cache's contract (packbin.go): a build arrives verified or not at all, keyed by its digest,
// executable; a second Ensure of the same pin is offline; a copy that stopped matching is
// replaced; a mismatch refuses naming both digests and leaves nothing behind.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// server serves body at /tool over TLS and counts the requests that reach it.
func server(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/tool" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// entries lists what is in dir, recursively, relative to it.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err == nil && p != dir {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func TestEnsureFetchesVerifiesAndCachesExecutable(t *testing.T) {
	body := []byte("#!/bin/sh\necho hello\n")
	srv, hits := server(t, body)
	dir := t.TempDir()
	f := Fetcher{Dir: dir, Client: srv.Client()}
	w := Want{Name: "tool", URL: srv.URL + "/tool", SHA256: digest(body)}

	path, outcome, err := f.Ensure(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != Fetched || path != Path(dir, w.SHA256, "tool") {
		t.Errorf("Ensure = %q, %v; want %q, Fetched", path, outcome, Path(dir, w.SHA256, "tool"))
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(body) {
		t.Fatalf("cached bytes = %q, %v", got, err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o555 || !Present(path) {
		t.Errorf("cached mode = %v, want 0555 (executable, writable by nobody)", fi.Mode().Perm())
	}
	// The same pin again is answered from the cache: re-hashed, not re-fetched.
	if _, outcome, err := f.Ensure(context.Background(), w); err != nil || outcome != Cached {
		t.Errorf("second Ensure = %v, %v; want Cached", outcome, err)
	}
	if hits.Load() != 1 {
		t.Errorf("the server saw %d requests, want 1: a cached pin must not touch the network", hits.Load())
	}
}

func TestEnsureReplacesACachedCopyThatNoLongerMatches(t *testing.T) {
	body := []byte("the real bytes")
	srv, hits := server(t, body)
	dir := t.TempDir()
	w := Want{Name: "tool", URL: srv.URL + "/tool", SHA256: digest(body)}
	final := Path(dir, w.SHA256, "tool")
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, outcome, err := Fetcher{Dir: dir, Client: srv.Client()}.Ensure(context.Background(), w)
	if err != nil || outcome != Replaced {
		t.Fatalf("Ensure = %v, %v; want Replaced", outcome, err)
	}
	if got, _ := os.ReadFile(final); string(got) != string(body) || hits.Load() != 1 {
		t.Errorf("after replacement the cache holds %q (%d fetches)", got, hits.Load())
	}
}

// A mismatch is an INTEGRITY failure: refused, both digests named, nothing cached, no temp left.
func TestEnsureRefusesADigestMismatchAndCachesNothing(t *testing.T) {
	srv, _ := server(t, []byte("what the server has"))
	dir := t.TempDir()
	want := digest([]byte("what the manifest pinned"))
	_, _, err := Fetcher{Dir: dir, Client: srv.Client()}.Ensure(context.Background(),
		Want{Name: "tool", URL: srv.URL + "/tool", SHA256: want})
	var ie *IntegrityError
	if !errors.As(err, &ie) {
		t.Fatalf("err = %v, want an IntegrityError", err)
	}
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), digest([]byte("what the server has"))) {
		t.Errorf("the refusal must name both digests: %v", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("a refused download left %q in the cache", got)
	}
}

func TestEnsureRefusesAFailedRequestAndAnOversizedFile(t *testing.T) {
	body := []byte("0123456789")
	srv, _ := server(t, body)
	dir := t.TempDir()
	f := Fetcher{Dir: dir, Client: srv.Client()}
	if _, _, err := f.Ensure(context.Background(), Want{Name: "tool", URL: srv.URL + "/gone",
		SHA256: digest(body)}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 = %v, want an error naming it", err)
	}
	f.MaxBytes = 4
	if _, _, err := f.Ensure(context.Background(), Want{Name: "tool", URL: srv.URL + "/tool",
		SHA256: digest(body)}); err == nil || !strings.Contains(err.Error(), "larger than 4 bytes") {
		t.Errorf("an oversized file = %v, want the bound named", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("failed downloads left %q in the cache", got)
	}
}

func TestPresentWantsAnExecutableRegularFile(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	if Present(plain) || Present(dir) || Present(filepath.Join(dir, "absent")) {
		t.Error("Present accepted a file without an exec bit, a directory or nothing")
	}
}
