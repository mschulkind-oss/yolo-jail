// Package packbin is the CACHE OF DOWNLOADED PACK BINARIES: the executables a loophole manifest
// declares under `binaries` (internal/loopholedecl's binaries.go), each fetched once, verified
// against the sha256 the manifest pins, and kept by digest with its exec bit set
// (docs/design/broker-as-a-pack.md BP-D1).
//
// # Layout
//
//	<dir>/<sha256>/<name>     one verified build, mode 0555
//
// The DIGEST is the key, so two packs pinning the same bytes share one file per name, and the
// NAME is kept as the file's own so a program that reads its argv[0] sees the name its manifest
// gave it. A file is written only by renaming a verified temp file into place, so a file under a
// digest is one that was verified against that digest when it arrived; nothing else writes here.
// The directory is paths.PackBinariesDir, which no jail mounts.
//
// # When it fetches
//
// Only when asked: `yolo pack install` (and `yolo pack update`, which runs it) calls Ensure for
// every build the selected packs need on this machine. A launch reads the cache through Path and
// Present and never fetches — broker-as-a-pack.md §3.1: "at `pack install`, never at launch" —
// so an offline launch of a pack whose builds are cached is an ordinary launch, and one whose
// builds are not reports the loophole inert and names the command.
//
// # What a digest mismatch is
//
// An INTEGRITY failure, not a fetch failure (§9's last risk): the file is refused, both digests
// are named, nothing is retried, and no copy already in the cache is used in its place.
//
// It depends on the standard library alone, so every reader of the cache — the loophole
// resolver, the pack verbs — can import it.
package packbin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DefaultMaxBytes bounds one download. The digest is checked only once the last byte has
// arrived, so without a bound a URL serving an endless stream would fill the disk before the
// refusal could happen. 512 MiB is several times any single program a loophole ships today.
const DefaultMaxBytes int64 = 512 << 20

// fileMode is a cached build's mode: executable, and writable by nobody, so nothing edits a
// verified file in place (a replacement arrives by rename, like the first copy).
const fileMode os.FileMode = 0o555

// Path is where the build with digest sum of the binary name lives under dir.
func Path(dir, sum, name string) string { return filepath.Join(dir, sum, name) }

// Present reports whether path is a cached build: a regular file with an exec bit.
func Present(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// Want is one build to put in the cache.
type Want struct {
	// Name is the binary's name, the cached file's own.
	Name string
	// URL is where to fetch it.
	URL string
	// SHA256 is the digest it must have, 64 lowercase hex digits.
	SHA256 string
}

// Outcome is what Ensure did.
type Outcome int

const (
	// Cached: a verified copy was already in place, and its bytes still match.
	Cached Outcome = iota
	// Fetched: the build was downloaded, verified and cached.
	Fetched
	// Replaced: a file under the digest no longer matched it, so it was removed and the build
	// downloaded again.
	Replaced
)

// IntegrityError is a download whose bytes do not match the pinned digest.
type IntegrityError struct {
	URL, Want, Got string
}

func (e *IntegrityError) Error() string {
	return fmt.Sprintf("%s served a file with sha256 %s, but the manifest pins %s — refused: "+
		"the digest is the pin, so a mismatch is not retried and no cached copy stands in for it",
		e.URL, e.Got, e.Want)
}

// Fetcher puts verified builds in the cache at Dir.
type Fetcher struct {
	// Dir is the cache root (paths.PackBinariesDir in production).
	Dir string
	// Client fetches. nil => an http.Client with a generous timeout.
	Client *http.Client
	// MaxBytes bounds one download. 0 => DefaultMaxBytes.
	MaxBytes int64
}

func (f Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return &http.Client{Timeout: 10 * time.Minute}
}

func (f Fetcher) maxBytes() int64 {
	if f.MaxBytes > 0 {
		return f.MaxBytes
	}
	return DefaultMaxBytes
}

// Ensure makes w's build present in the cache, verified, and returns its path and what it did.
//
// A copy already under the digest is hashed again rather than trusted by name, so a reinstall
// is also a check: it answers Cached without the network when the bytes still match, and
// Replaced after a fresh download when they do not.
func (f Fetcher) Ensure(ctx context.Context, w Want) (string, Outcome, error) {
	if f.Dir == "" {
		return "", 0, errors.New("no pack-binary cache directory")
	}
	final := Path(f.Dir, w.SHA256, w.Name)
	outcome := Fetched
	if Present(final) {
		got, err := hashFile(final)
		if err == nil && got == w.SHA256 {
			return final, Cached, nil
		}
		if err := os.Remove(final); err != nil {
			return "", 0, fmt.Errorf("removing %s, which no longer matches its digest: %w", final, err)
		}
		outcome = Replaced
	}
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return "", 0, err
	}
	tmp, got, err := f.download(ctx, w.URL)
	if tmp != "" {
		defer os.Remove(tmp) // a no-op once it has been renamed into place
	}
	if err != nil {
		return "", 0, fmt.Errorf("fetching %s: %w", w.URL, err)
	}
	if got != w.SHA256 {
		return "", 0, &IntegrityError{URL: w.URL, Want: w.SHA256, Got: got}
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", 0, err
	}
	return final, outcome, nil
}

// download streams url into a temp file under the cache root, hashing as it goes. The temp file
// is in the cache's own directory so the rename that admits it is atomic.
func (f Fetcher) download(ctx context.Context, url string) (file, sum string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", errors.New(resp.Status)
	}
	out, err := os.CreateTemp(f.Dir, ".fetch-*")
	if err != nil {
		return "", "", err
	}
	limit := f.maxBytes()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return out.Name(), "", err
	}
	if n > limit {
		return out.Name(), "", fmt.Errorf("the file is larger than %d bytes, the most one "+
			"download may be", limit)
	}
	return out.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// hashFile is the sha256 of a file's bytes.
func hashFile(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := sha256.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
