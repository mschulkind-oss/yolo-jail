package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Toolchain is the Go every official build is made with (BP-D9): Go's own distribution of this
// version, never whatever `go` the machine's PATH names, because the toolchain's version is in
// the bytes and nothing else ties the maintainer's Go to the release runner's. It is at or above
// go.mod's `go` line (TestToolchainIsAtOrAboveGoMod), and moving it is a commit that re-pins
// every build: run `just pin-pack-binaries <version>` in the same change.
const Toolchain = "go1.26.7"

// toolchainModule is the module the go command itself downloads to switch to Toolchain on a
// machine of goos/goarch (`GOTOOLCHAIN`): Go's release, served by the module proxy.
func toolchainModule(goos, goarch string) string {
	return "golang.org/toolchain@v0.0.1-" + Toolchain + "." + goos + "-" + goarch
}

// fetchToolchain returns the go binary of Toolchain for this machine, downloading it the way
// GOTOOLCHAIN does: `go mod download` of the golang.org/toolchain module, through the module
// proxy, checked against the checksum database, into the module cache, where a later run finds
// it. hostGo is the go on PATH, used only to download.
//
// The download ignores the go env file (GOENV=off): an empty environment variable does not
// override a setting in that file, so it is the only way to be sure no GONOSUMDB, GOPRIVATE,
// GOINSECURE or GOFLAGS there weakens the check. GOSUMDB=off is overridden for the same
// reason; a GOSUMDB naming another database, and GOPROXY, are honored — from the environment or
// from that file, read first with `go env` and passed explicitly (effectiveProxy, BP-D27).
func fetchToolchain(hostGo string, environ []string) (string, error) {
	tmp, err := os.MkdirTemp("", "pack-binaries-toolchain-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	mod := toolchainModule(runtime.GOOS, runtime.GOARCH)
	proxy, sumdb := effectiveProxy(hostGo, environ, tmp)
	cmd := exec.Command(hostGo, "mod", "download", "-json", mod)
	// Outside every module, so no go.mod, go.work or vendor/ decides anything.
	cmd.Dir = tmp
	cmd.Env = downloadEnv(environ, proxy, sumdb)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, runErr := cmd.Output()
	var info struct {
		Dir, Sum, Error string
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("downloading %s: %v %s", mod, runErr, strings.TrimSpace(stderr.String()))
	}
	if info.Error != "" || runErr != nil {
		return "", fmt.Errorf("downloading %s: %s %s", mod, info.Error, strings.TrimSpace(stderr.String()))
	}
	if info.Dir == "" || info.Sum == "" {
		return "", fmt.Errorf("downloading %s: the go command named no directory or checksum", mod)
	}
	if err := allowExec(info.Dir); err != nil {
		return "", fmt.Errorf("making %s runnable: %w", info.Dir, err)
	}
	goBin := filepath.Join(info.Dir, "bin", "go")
	if err := checkToolchainVersion(goBin, environ); err != nil {
		return "", err
	}
	return goBin, nil
}

// checkToolchainVersion refuses a go that does not report exactly Toolchain for this machine.
func checkToolchainVersion(goBin string, environ []string) error {
	vcmd := exec.Command(goBin, "version")
	vcmd.Env = buildEnv(environ, runtime.GOOS, runtime.GOARCH)
	vout, err := vcmd.Output()
	if err != nil {
		return fmt.Errorf("%s version: %w", goBin, err)
	}
	want := "go version " + Toolchain + " " + runtime.GOOS + "/" + runtime.GOARCH
	if got := strings.TrimSpace(string(vout)); got != want {
		return fmt.Errorf("%s reports %q, want %q", goBin, got, want)
	}
	return nil
}

// effectiveProxy is the GOPROXY and GOSUMDB the go on PATH would use — the environment first,
// then the go env file `go env -w` writes — asked of it with `go env`, because the download runs
// with GOENV=off and so would otherwise ignore a proxy set there (BP-D27). The question runs
// outside every module, never switches toolchains to answer (GOTOOLCHAIN=local), and carries no
// GOFLAGS that could fail it. "" for either when it cannot be answered, and downloadEnv falls back
// to the environment's.
func effectiveProxy(hostGo string, environ []string, dir string) (proxy, sumdb string) {
	cmd := exec.Command(hostGo, "env", "-json", "GOPROXY", "GOSUMDB")
	cmd.Dir = dir
	var env []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if k != "GOFLAGS" && k != "GOTOOLCHAIN" && k != "GOWORK" {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := cmd.Output()
	if err != nil {
		return "", ""
	}
	var v struct{ GOPROXY, GOSUMDB string }
	if json.Unmarshal(out, &v) != nil {
		return "", ""
	}
	return v.GOPROXY, v.GOSUMDB
}

// downloadEnv is environ with every setting that could skip the checksum database removed, the
// download pinned to the go on PATH (GOTOOLCHAIN=local) outside any workspace, and the proxy and
// checksum database the user's go would use (effectiveProxy) passed explicitly, since GOENV=off
// hides the go env file. A GOSUMDB of "off", or none, is sum.golang.org; "" for proxy or sumdb
// falls back to the environment's own.
func downloadEnv(environ []string, proxy, sumdb string) []string {
	drop := map[string]bool{"GOENV": true, "GOFLAGS": true, "GONOSUMDB": true,
		"GONOSUMCHECK": true, "GOPRIVATE": true, "GONOPROXY": true, "GOINSECURE": true,
		"GOTOOLCHAIN": true, "GOWORK": true, "GOSUMDB": true, "GOPROXY": true}
	var out []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "GOSUMDB" && sumdb == "":
			sumdb = v
		case k == "GOPROXY" && proxy == "":
			proxy = v
		}
		if !drop[k] {
			out = append(out, kv)
		}
	}
	if sumdb == "" || sumdb == "off" {
		sumdb = "sum.golang.org"
	}
	out = append(out, "GOENV=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOSUMDB="+sumdb)
	if proxy != "" {
		out = append(out, "GOPROXY="+proxy)
	}
	return out
}

// allowExec sets the execute bits the module cache does not keep, on the files the go command
// runs, in the order the go command's own toolchain switch sets them (cmd/go/internal/toolchain):
// the tools and the exec wrappers before bin/, so a racing go command that sees bin/go runnable
// never runs a tool that is not yet. A no-op once bin/go is executable.
func allowExec(dir string) error {
	fi, err := os.Stat(filepath.Join(dir, "bin", "go"))
	if err != nil {
		return err
	}
	if fi.Mode()&0o111 != 0 {
		return nil
	}
	walk := func(root, pattern string) error {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if pattern != "" {
				if ok, _ := filepath.Match(pattern, d.Name()); !ok {
					return nil
				}
			}
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			return os.Chmod(p, info.Mode()&0o777|0o111)
		})
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, step := range []struct{ root, pattern string }{
		{filepath.Join(dir, "pkg", "tool"), ""},
		{filepath.Join(dir, "lib"), "go_?*_?*_exec"},
		{filepath.Join(dir, "bin", "gofmt"), ""},
		{filepath.Join(dir, "bin"), ""},
	} {
		if err := walk(step.root, step.pattern); err != nil {
			return err
		}
	}
	return nil
}
