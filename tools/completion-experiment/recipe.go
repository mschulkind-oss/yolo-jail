package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/releasematrix"
)

// THE RECIPE (docs/design/broker-as-a-pack.md BP-D9) is how every official build is made, so a
// build of the same tree gives the same bytes on any machine: §14.2 measured that the checkout's
// path and the build cache do not move them, and each item below removes one thing that does.
//
//   - Toolchain, never the PATH's go: the toolchain's version is in the bytes.
//   - cgo off: the release cross-compiles every platform on one Linux runner, where cgo for
//     darwin cannot build.
//   - -trimpath: no checkout path in the bytes.
//   - -buildvcs=false and no -ldflags: nothing is stamped. A VCS or commit stamp names the
//     commit that holds the digest, so no build could match it; a version stamp would move
//     every digest on every release and turn every upgraded machine's loophole off until
//     `yolo pack install`.
//   - -mod=vendor: the committed vendor/, with no module download.
//   - GOENV=off, and every other GO* and CGO_* variable dropped: no GOFLAGS, no GOEXPERIMENT, no
//     GOFIPS140, no GOROOT pointing at another toolchain's standard library.
//   - Each architecture level at Go's default, GOAMD64=v1 and GOARM64=v8.0, written out: a
//     level changes the code the compiler emits.
//
// A build is always written under the binary's own name, which is the name the cache keeps it
// under too (internal/packbin), so a program reading its argv[0] sees one name everywhere.

// keptGoVars are the GO* variables the recipe passes through: each says only where a cache or
// a scratch directory is, which moves no byte of the output.
var keptGoVars = map[string]bool{"GOCACHE": true, "GOMODCACHE": true, "GOPATH": true, "GOTMPDIR": true}

// archLevel is the variable naming each architecture's level, and Go's default for it.
var archLevel = map[string]string{"amd64": "GOAMD64=v1", "arm64": "GOARM64=v8.0"}

// buildEnv is environ as the recipe builds for goos/goarch.
func buildEnv(environ []string, goos, goarch string) []string {
	var out []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if (strings.HasPrefix(k, "GO") && !keptGoVars[k]) || strings.HasPrefix(k, "CGO_") {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "CGO_ENABLED=0",
		"GOOS="+goos, "GOARCH="+goarch)
	if lvl, ok := archLevel[goarch]; ok {
		out = append(out, lvl)
	}
	return out
}

// buildArgs is the recipe's go command line, writing the program to out.
func buildArgs(out, name string) []string {
	return []string{"build", "-trimpath", "-buildvcs=false", "-mod=vendor", "-o", out,
		"./" + releasematrix.ProgramDir(name)}
}

// buildOne builds cmd/<name> for platform with the recipe, under outDir/<goos>-<goarch>/<name>,
// and returns the file and its sha256.
func buildOne(goBin, root string, environ []string, name, platform, outDir string) (string, string, error) {
	goos, goarch, _ := strings.Cut(platform, "/")
	out := filepath.Join(outDir, goos+"-"+goarch, name)
	cmd := exec.Command(goBin, buildArgs(out, name)...)
	cmd.Dir = root
	cmd.Env = buildEnv(environ, goos, goarch)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("building %s for %s: %v\n%s", releasematrix.ProgramDir(name),
			platform, err, strings.TrimSpace(output.String()))
	}
	sum, err := fileSHA256(out)
	return out, sum, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
