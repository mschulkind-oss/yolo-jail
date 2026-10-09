package main

import (
	"bytes"
	"context"
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
	return fetchToolchainObserved(context.Background(), hostGo, environ, nil)
}

// fetchToolchainObserved is the same acquisition path as fetchToolchain, with a private session
// observing each command at its Start/Wait boundary and holding the successful return until the
// controller releases it. A nil session keeps the ordinary API and behavior.
func fetchToolchainObserved(ctx context.Context, hostGo string, environ []string, session *toolchainSession) (goBin string, err error) {
	tmp, err := os.MkdirTemp("", "pack-binaries-toolchain-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	mod := toolchainModule(runtime.GOOS, runtime.GOARCH)
	proxy, sumdb := "", ""
	var captured toolchainObservation
	captured.Protocol = toolchainProtocolVersion
	captured.HostOS, captured.HostArch = runtime.GOOS, runtime.GOARCH
	captured.Downloader = hostGo
	captured.Module = mod
	captured.Toolchain = Toolchain
	stage := "effective-proxy"
	defer func() {
		if session != nil && err != nil {
			session.fail(stage, captured, "acquisition_failed")
		}
	}()

	if session == nil {
		proxy, sumdb = effectiveProxy(hostGo, environ, tmp)
	} else {
		proxy, sumdb, captured.Proxy, err = observedEffectiveProxy(ctx, hostGo, environ, tmp, session)
		if errors.Is(err, errObservedOutputOverflow) {
			stage = "effective-proxy-overflow"
			return "", err
		}
		if err != nil {
			stage = "effective-proxy"
			return "", err
		}
	}

	stage = "download"
	cmd := exec.Command(hostGo, "mod", "download", "-json", mod)
	// Outside every module, so no go.mod, go.work or vendor/ decides anything.
	cmd.Dir = tmp
	downloadEnv := downloadEnv(environ, proxy, sumdb)
	cmd.Env = downloadEnv
	var stderr bytes.Buffer
	var out []byte
	if session == nil {
		cmd.Stderr = &stderr
		out, err = cmd.Output()
	} else {
		var observed commandObservation
		observed, err = session.runCommand(ctx, "download", cmd, downloadOutputLimit)
		captured.Commands = append(captured.Commands, observed)
		captured.DownloadContext = observed.Environment
		out, stderr = observed.Stdout, bytes.Buffer{}
		stderr.Write(observed.Stderr)
	}
	var info struct {
		Dir, Sum, Error string
	}
	if err != nil && session != nil && errors.Is(err, errObservedOutputOverflow) {
		stage = "download-overflow"
		return "", err
	}
	if err != nil && session != nil && ctx.Err() != nil {
		stage = "download-canceled"
		return "", ctx.Err()
	}
	if unmarshalErr := json.Unmarshal(out, &info); unmarshalErr != nil {
		stage = "download-result"
		return "", fmt.Errorf("downloading %s: %v %s", mod, err, strings.TrimSpace(stderr.String()))
	}
	if info.Error != "" || err != nil {
		stage = "download-result"
		return "", fmt.Errorf("downloading %s: %s %s", mod, info.Error, strings.TrimSpace(stderr.String()))
	}
	if info.Dir == "" || info.Sum == "" {
		stage = "download-result"
		return "", fmt.Errorf("downloading %s: the go command named no directory or checksum", mod)
	}
	captured.ChosenDir, captured.ModuleSum = info.Dir, info.Sum

	readyGo := filepath.Join(info.Dir, "bin", "go")
	wasRunnable := false
	if session != nil {
		before, beforeErr := os.Stat(readyGo)
		wasRunnable = beforeErr == nil && before.Mode()&0o111 != 0
	}
	stage = "readiness"
	var allowExecErr error
	if session == nil {
		allowExecErr = allowExec(info.Dir)
	} else {
		allowExecErr = allowExecContext(ctx, info.Dir)
	}
	if allowExecErr != nil {
		captured.Readiness = readinessObservation{Directory: info.Dir, GoBinary: readyGo, Status: "failed"}
		return "", fmt.Errorf("making %s runnable: %w", info.Dir, allowExecErr)
	}
	goBin = readyGo
	readyStatus := "success"
	if wasRunnable {
		readyStatus = "no-op"
	}
	captured.Readiness = readinessObservation{Directory: info.Dir, GoBinary: goBin, Status: readyStatus}

	stage = "version"
	if session == nil {
		if err := checkToolchainVersion(goBin, environ); err != nil {
			return "", err
		}
	} else {
		versionCmd := exec.Command(goBin, "version")
		versionCmd.Env = buildEnv(environ, runtime.GOOS, runtime.GOARCH)
		observed, versionErr := session.runCommand(ctx, "version", versionCmd, versionOutputLimit)
		captured.Commands = append(captured.Commands, observed)
		captured.VersionContext = observed.Environment
		captured.VersionOutput = observed.Stdout
		if versionErr != nil {
			if errors.Is(versionErr, errObservedOutputOverflow) {
				stage = "version-overflow"
			} else if ctx.Err() != nil {
				stage = "version-canceled"
			}
			return "", fmt.Errorf("%s version: %w", goBin, versionErr)
		}
		want := "go version " + Toolchain + " " + runtime.GOOS + "/" + runtime.GOARCH
		if got := strings.TrimSpace(string(observed.Stdout)); got != want {
			stage = "version-result"
			return "", fmt.Errorf("%s reports %q, want %q", goBin, got, want)
		}
	}
	captured.ReturnedGo = goBin
	if session != nil {
		stage = "return-release"
		if err := session.returnAndWait(ctx, captured); err != nil {
			return "", err
		}
	}
	return goBin, nil
}

func observedEffectiveProxy(ctx context.Context, hostGo string, environ []string, dir string, session *toolchainSession) (proxy, sumdb string, observed proxyObservation, err error) {
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
	captured, runErr := session.runCommand(ctx, "effective-proxy", cmd, effectiveProxyOutputLimit)
	observed.Command = captured
	if errors.Is(runErr, errObservedOutputOverflow) {
		return "", "", observed, runErr
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return "", "", observed, ctx.Err()
		}
		observed.Fallback = "command-failed"
		return "", "", observed, nil
	}
	var values struct{ GOPROXY, GOSUMDB string }
	if json.Unmarshal(captured.Stdout, &values) != nil {
		observed.Fallback = "invalid-json"
		return "", "", observed, nil
	}
	observed.Proxy, observed.SumDB = values.GOPROXY, values.GOSUMDB
	observed.Fallback = "none"
	if values.GOPROXY == "" || values.GOSUMDB == "" {
		observed.Fallback = "environment-fallback"
	}
	return values.GOPROXY, values.GOSUMDB, observed, nil
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
	return allowExecContext(context.Background(), dir)
}

func allowExecContext(ctx context.Context, dir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := os.Stat(filepath.Join(dir, "bin", "go"))
	if err != nil {
		return err
	}
	if fi.Mode()&0o111 != 0 {
		return nil
	}
	walk := func(root, pattern string) error {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
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
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := walk(step.root, step.pattern); err != nil {
			return err
		}
	}
	return nil
}
