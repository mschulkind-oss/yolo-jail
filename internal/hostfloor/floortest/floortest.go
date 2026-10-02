// Package floortest is the fake world the host agent floor's tests run against, shared by the
// floor's own tests (internal/hostfloor) and the host verbs' (internal/cli): a Node "release"
// served from a local HTTP server, whose tarball carries a shell-script `node` and `npm`, and a
// fake npm registry that is a directory of files. Nothing here touches the real internet, a real
// home, or a real agent — the rules AGENTS.md sets for every test in this repository.
//
// The fake npm understands the two verbs the floor uses:
//
//	npm install -g [flags] <name>@<selector>   writes <prefix>/lib/node_modules/<name> and
//	                                           links <prefix>/bin/<bin> at its entry script
//	npm view <name> version                    prints the registry's "latest"
//
// and records every invocation in the dist's NpmLog, one line each, so a test can count installs.
// Per package, the registry directory holds (name with / replaced by _):
//
//	<name>.latest   the version "latest" resolves to (required)
//	<name>.bin      the bin name it provides (default: the name after its scope)
//	<name>.native   present: the bin is a binary blob, not a Node script
//	<name>.fail     present: every install of it fails
//	<name>.sleep    seconds an install takes
//
// The fake node prints `node:<its argv>`, so a test can see exactly what the floor started.
//
// It is an ordinary package so both test suites can import it; nothing in production does.
package floortest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// NodeScript is the fake `node`.
const NodeScript = `#!/bin/sh
# fake node: prints what it was asked to run, so a test can see the argv the floor built
printf 'node:%s\n' "$*"
`

// NpmScript is the fake `npm`.
const NpmScript = `#!/bin/sh
# fake npm for the host agent floor's tests
set -e
printf '%s\n' "$*" >> "$FAKE_NPM_LOG"
reg="$FAKE_NPM_REGISTRY"
verb="$1"; shift
esc() { printf '%s' "$1" | tr / _; }
case "$verb" in
view)
    cat "$reg/$(esc "$1").latest"
    ;;
install)
    for a in "$@"; do spec="$a"; done
    case "$spec" in
    @*) rest="${spec#@}"; name="@${rest%@*}"; sel="${rest##*@}" ;;
    *) name="${spec%@*}"; sel="${spec##*@}" ;;
    esac
    key="$(esc "$name")"
    if [ -f "$reg/$key.sleep" ]; then sleep "$(cat "$reg/$key.sleep")"; fi
    if [ -f "$reg/$key.fail" ]; then echo "npm ERR! 404 $name is not in this registry" >&2; exit 1; fi
    if [ "$sel" = latest ]; then ver="$(cat "$reg/$key.latest")"; else ver="$sel"; fi
    bin="${name##*/}"
    if [ -f "$reg/$key.bin" ]; then bin="$(cat "$reg/$key.bin")"; fi
    pkg="$NPM_CONFIG_PREFIX/lib/node_modules/$name"
    mkdir -p "$pkg/bin" "$NPM_CONFIG_PREFIX/bin"
    printf '{"name":"%s","version":"%s"}\n' "$name" "$ver" > "$pkg/package.json"
    if [ -f "$reg/$key.native" ]; then
        printf '\177ELF\002\001\001\000native-%s\n' "$ver" > "$pkg/bin/cli"
    else
        printf '#!/usr/bin/env node\nconsole.log("%s %s")\n' "$name" "$ver" > "$pkg/bin/cli"
    fi
    chmod 755 "$pkg/bin/cli"
    ln -sf "../lib/node_modules/$name/bin/cli" "$NPM_CONFIG_PREFIX/bin/$bin"
    echo "added 1 package in 0s"
    ;;
*)
    echo "fake npm: unknown verb $verb" >&2; exit 2
    ;;
esac
`

// Shipped is the release a Dist serves as the "shipped" one, pinned by digest; Raised is a second
// release it serves with a published SHASUMS256.txt, for a node_floor to raise the floor to.
const (
	Shipped = "24.0.0"
	Raised  = "99.1.0"
)

// Dist is one test's fake Node distribution and npm registry.
type Dist struct {
	// URL is the distribution root the floor's NodeDist.BaseURL takes.
	URL string
	// GOOS and GOARCH are the Go platform the distribution serves a Node release for. A test floor
	// reading it takes its own GOOS and GOARCH from here, so the release the floor asks for is the
	// one served even when the test puts the floor on a platform the machine running it is not.
	GOOS, GOARCH string
	// Platform is Node's name for GOOS/GOARCH ("linux-x64").
	Platform string
	// SHA256 is the shipped tarball's digest, for NodeDist.Pinned.
	SHA256 string
	// Registry and NpmLog are the fake npm's registry directory and invocation log.
	Registry, NpmLog string
	t                *testing.T
}

// Platform is Node's platform name for a Go platform, and whether Node publishes one.
func Platform(goos, goarch string) (string, bool) {
	if goos != "linux" && goos != "darwin" {
		return "", false
	}
	switch goarch {
	case "amd64":
		return goos + "-x64", true
	case "arm64":
		return goos + "-arm64", true
	}
	return "", false
}

// ResolvedTemp is t.TempDir() with its symlinks resolved where it is minted, so a path the floor
// records compares equal on darwin, whose TMPDIR sits under /var -> /private/var.
func ResolvedTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// NewDist starts a fake distribution serving Shipped and Raised for this machine's platform.
func NewDist(t *testing.T) *Dist {
	t.Helper()
	return NewDistOn(t, runtime.GOOS, runtime.GOARCH)
}

// NewLinuxDist is NewDistOn for Linux on this machine's architecture: the platform a test puts
// its floor on to hold a fork's build or an installer agent's capture (both made in a Linux jail,
// and a floor holds a build made for its own platform only) whatever machine runs the test. The
// floor must then take its GOOS and GOARCH from this distribution, not from the runtime.
func NewLinuxDist(t *testing.T) *Dist {
	t.Helper()
	return NewDistOn(t, "linux", runtime.GOARCH)
}

// NewDistOn starts a fake distribution serving Shipped and Raised for goos/goarch, which need not
// be this machine's: the fake node and npm are shell scripts, so they run wherever /bin/sh does.
func NewDistOn(t *testing.T, goos, goarch string) *Dist {
	t.Helper()
	plat, ok := Platform(goos, goarch)
	if !ok {
		t.Skipf("no Node platform name for %s/%s", goos, goarch)
	}
	root := ResolvedTemp(t)
	d := &Dist{GOOS: goos, GOARCH: goarch, Platform: plat,
		Registry: filepath.Join(root, "registry"), NpmLog: filepath.Join(root, "npm.log"), t: t}
	if err := os.MkdirAll(d.Registry, 0o755); err != nil {
		t.Fatal(err)
	}
	releases := map[string][]byte{Shipped: NodeTarball(t, Shipped, plat), Raised: NodeTarball(t, Raised, plat)}
	d.SHA256 = Sum(releases[Shipped])
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		for v, body := range releases {
			switch r.URL.Path {
			case "/dist/v" + v + "/node-v" + v + "-" + plat + ".tar.gz":
				_, _ = rw.Write(body)
				return
			case "/dist/v" + v + "/SHASUMS256.txt":
				fmt.Fprintf(rw, "%s  node-v%s-%s.tar.gz\n", Sum(body), v, plat)
				return
			}
		}
		http.NotFound(rw, r)
	}))
	t.Cleanup(srv.Close)
	d.URL = srv.URL + "/dist"
	return d
}

// Environ is what a floor's Environ must carry for the fake npm to find its registry and log.
func (d *Dist) Environ() []string {
	return []string{"FAKE_NPM_REGISTRY=" + d.Registry, "FAKE_NPM_LOG=" + d.NpmLog}
}

// Publish puts a package in the registry at version latest, with options: "native", "fail",
// "bin=<name>", "sleep=<seconds>". Publishing again replaces "native" and "fail".
func (d *Dist) Publish(name, latest string, opts ...string) {
	d.t.Helper()
	key := strings.ReplaceAll(name, "/", "_")
	if err := os.WriteFile(filepath.Join(d.Registry, key+".latest"), []byte(latest+"\n"), 0o644); err != nil {
		d.t.Fatal(err)
	}
	for _, o := range []string{"native", "fail"} {
		_ = os.Remove(filepath.Join(d.Registry, key+"."+o))
	}
	for _, o := range opts {
		k, v, _ := strings.Cut(o, "=")
		if err := os.WriteFile(filepath.Join(d.Registry, key+"."+k), []byte(v), 0o644); err != nil {
			d.t.Fatal(err)
		}
	}
}

// NpmCalls returns the fake npm's invocation lines that start with verb.
func (d *Dist) NpmCalls(verb string) []string {
	b, err := os.ReadFile(d.NpmLog)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, verb+" ") {
			out = append(out, l)
		}
	}
	return out
}

// NodeTarball builds a Node release tarball for version on plat, wrapped the way Node wraps one,
// with npm a RELATIVE symlink into lib/ as in a real release.
func NodeTarball(t *testing.T, version, plat string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := "node-v" + version + "-" + plat + "/"
	write := func(hdr *tar.Header, body string) {
		hdr.Name = top + hdr.Name
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(&tar.Header{Name: "", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	write(&tar.Header{Name: "bin/", Typeflag: tar.TypeDir, Mode: 0o755}, "")
	write(&tar.Header{Name: "bin/node", Typeflag: tar.TypeReg, Mode: 0o755}, NodeScript)
	write(&tar.Header{Name: "lib/node_modules/npm/bin/npm-cli.js", Typeflag: tar.TypeReg, Mode: 0o755}, NpmScript)
	write(&tar.Header{Name: "bin/npm", Typeflag: tar.TypeSymlink,
		Linkname: "../lib/node_modules/npm/bin/npm-cli.js", Mode: 0o777}, "")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Sum is the hex sha256 of b.
func Sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
