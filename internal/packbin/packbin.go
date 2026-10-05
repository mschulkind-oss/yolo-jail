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
// gave it. A file is written only by renaming a verified temp file into place (admit), so a file
// under a digest is one that was verified against that digest when it arrived; nothing else
// writes here. The directory is paths.PackBinariesDir, which no jail mounts.
//
// # How a build arrives
//
// Two ways, through the one verified rename:
//
//   - FETCHED, by Ensure: `yolo pack install` (and `yolo pack update`, which runs it) calls it
//     for every build the selected packs need on this machine.
//   - SEEDED, by Seed: `just install` builds this machine's builds of every official pack
//     program from the source tree it installs, and admits each whose digest is the pin, so a
//     from-source or forked tree's jail runs that tree's programs with no download
//     (broker-as-a-pack.md BP-D15, OQ-BP7 ruled 2026-10-05). The integration harness seeds its
//     run's cache the same way. Both run the pin tool (tools/pack-binaries), which builds.
//
// A launch reads the cache through Path and Present and NEVER FETCHES — broker-as-a-pack.md
// §3.1: "at `pack install`, never at launch" — so an offline launch of a pack whose builds are
// cached is an ordinary launch, and one whose builds are not reports the loophole inert and
// names the command.
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
	// downloaded (or, from Seed, copied) again.
	Replaced
	// Seeded: the build was copied in from a file this machine built (Seed), verified, and
	// cached. Nothing was downloaded.
	Seeded
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

// maxRedirects is Go's own default bound, restated because setting CheckRedirect replaces it.
const maxRedirects = 10

// client is the Fetcher's client with HTTPS HELD ACROSS REDIRECTS: the manifest's URL is https
// (loopholedecl refuses any other), but Go's client follows a redirect to plain http by default,
// which would send the request for the build over the network in the clear. A copy, so a caller's
// client is not changed; a CheckRedirect the caller set still runs after this one.
func (f Fetcher) client() *http.Client {
	c := http.Client{Timeout: 10 * time.Minute}
	if f.Client != nil {
		c = *f.Client
	}
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("redirected to %s, which is not https — a pack binary is "+
				"fetched over https only, redirects included", req.URL.Redacted())
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		return nil
	}
	return &c
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
	if err := admit(tmp, final); err != nil {
		return "", 0, err
	}
	return final, outcome, nil
}

// admit is THE VERIFIED RENAME, the only way a file enters the cache: tmp, a temp file in the
// cache root whose bytes were hashed as they were written and matched the digest final is kept
// under, is made 0555 and renamed into place. The rename is atomic because tmp is in the cache's
// own directory tree, so a reader sees either no build or the whole verified one.
func admit(tmp, final string) error {
	if err := os.Chmod(tmp, fileMode); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// SeedMismatchError is a file offered to Seed whose bytes do not match the digest it was offered
// under.
type SeedMismatchError struct {
	Src, Want, Got string
}

func (e *SeedMismatchError) Error() string {
	return fmt.Sprintf("%s has sha256 %s, but the manifest pins %s — refused: only a build "+
		"whose digest is the pin enters the cache", e.Src, e.Got, e.Want)
}

// Seed admits a build this machine made — the pin tool's build of an official pack program from
// the source tree — to the cache at dir, under digest sum and binary name, and returns its path
// and what it did: Seeded, Cached when a copy that still matches is already there, or Replaced
// when one that no longer matches was. NOTHING IS DOWNLOADED.
//
// src is COPIED, never moved or linked, and the digest is checked over the bytes as they are
// written into the cache's own temp file, not over src: what is admitted is exactly what was
// hashed, whatever happens to src meanwhile. A mismatch is a *SeedMismatchError and caches
// nothing. The rename that admits it is Ensure's (admit), so a seeded build and a fetched one are
// indistinguishable to every reader, which is the point: the launch finds the tree's build where
// it would find a release's.
func Seed(dir, name, sum, src string) (string, Outcome, error) {
	if dir == "" {
		return "", 0, errors.New("no pack-binary cache directory")
	}
	if !validDigest(sum) {
		return "", 0, fmt.Errorf("%q is not a sha256 (64 lowercase hex digits)", sum)
	}
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return "", 0, fmt.Errorf("%q is not a binary name", name)
	}
	final := Path(dir, sum, name)
	outcome := Seeded
	if Present(final) {
		got, err := hashFile(final)
		if err == nil && got == sum {
			return final, Cached, nil
		}
		if err := os.Remove(final); err != nil {
			return "", 0, fmt.Errorf("removing %s, which no longer matches its digest: %w", final, err)
		}
		outcome = Replaced
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	tmp, got, err := copyHashed(dir, src)
	if tmp != "" {
		defer os.Remove(tmp) // a no-op once it has been renamed into place
	}
	if err != nil {
		return "", 0, fmt.Errorf("copying %s into the cache: %w", src, err)
	}
	if got != sum {
		return "", 0, &SeedMismatchError{Src: src, Want: sum, Got: got}
	}
	if err := admit(tmp, final); err != nil {
		return "", 0, err
	}
	return final, outcome, nil
}

// copyHashed copies src into a temp file under the cache root, hashing the bytes as they are
// written, for the same reason download does.
func copyHashed(dir, src string) (file, sum string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", "", err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return "", "", err
	}
	if !fi.Mode().IsRegular() {
		return "", "", errors.New("not a regular file")
	}
	out, err := os.CreateTemp(dir, ".seed-*")
	if err != nil {
		return "", "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return out.Name(), "", err
	}
	return out.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

// validDigest reports whether s is a sha256 as the manifest spells one, so a caller's string can
// name a directory under the cache root and nothing else.
func validDigest(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// download streams url into a temp file under the cache root, hashing as it goes. The temp file
// is in the cache's own directory so the rename that admits it is atomic.
func (f Fetcher) download(ctx context.Context, url string) (file, sum string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", "", err
	}
	// The manifest decoder refuses a non-https URL already; this holds the rule for any caller.
	if req.URL.Scheme != "https" {
		return "", "", errors.New("not an https URL — a pack binary is fetched over https only")
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
