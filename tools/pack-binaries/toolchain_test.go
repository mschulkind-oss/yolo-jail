package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeToolchain lays out a downloaded toolchain as the module cache leaves it: every file
// read-only and none executable. Its bin/go answers `version` with the version given.
func fakeToolchain(t *testing.T, version string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "golang.org", "toolchain@v0.0.1-"+Toolchain)
	files := map[string]string{
		"bin/go":    "#!/bin/sh\necho 'go version " + version + " " + runtime.GOOS + "/" + runtime.GOARCH + "'\n",
		"bin/gofmt": "#!/bin/sh\n",
		"pkg/tool/" + runtime.GOOS + "_" + runtime.GOARCH + "/compile": "#!/bin/sh\n",
		"lib/wasm/go_js_wasm_exec":                                     "#!/bin/sh\n",
		"lib/time/zoneinfo.zip":                                        "not a program",
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o444); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// stubGo is a `go` that records how it was run and answers `go mod download -json` with answer.
func stubGo(t *testing.T, answer any) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	if err := os.Mkdir(log, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(log, "answer"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "go")
	// `go env` is answered from goenv (written by goEnvAnswer; {} when there is none) and logged
	// apart, so args and env stay the download's.
	script := "#!/bin/sh\nif [ \"$1\" = env ]; then printf '%s\\n' \"$@\" > '" + log + "/envargs'\n" +
		"env > '" + log + "/envenv'\ncat '" + log + "/goenv' 2>/dev/null || echo '{}'\nexit 0\nfi\n" +
		"printf '%s\\n' \"$@\" > '" + log + "/args'\npwd > '" + log + "/cwd'\n" +
		"env > '" + log + "/env'\ncat '" + log + "/answer'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func stubLog(t *testing.T, log, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(log, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The toolchain is downloaded the way GOTOOLCHAIN downloads it — `go mod download` of the
// golang.org/toolchain module, outside any module, with nothing able to skip the checksum
// database — then made runnable as the go command makes it, and refused unless it is the pinned
// version.
func TestFetchToolchainDownloadsTheModuleAndChecksIt(t *testing.T) {
	tc := fakeToolchain(t, Toolchain)
	g, log := stubGo(t, map[string]string{"Dir": tc, "Sum": "h1:fake="})
	environ := append(os.Environ(), "GOSUMDB=off", "GONOSUMDB=golang.org", "GOPRIVATE=golang.org",
		"GOINSECURE=golang.org", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=auto", "GOENV=/somewhere/env")
	goBin, err := fetchToolchain(g, environ)
	if err != nil {
		t.Fatal(err)
	}
	if goBin != filepath.Join(tc, "bin", "go") {
		t.Errorf("fetchToolchain = %s, want the module's bin/go", goBin)
	}

	args := strings.Fields(stubLog(t, log, "args"))
	want := []string{"mod", "download", "-json", toolchainModule(runtime.GOOS, runtime.GOARCH)}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("the download ran `go %v`, want `go %v`", args, want)
	}
	if cwd, _ := os.Getwd(); strings.TrimSpace(stubLog(t, log, "cwd")) == cwd {
		t.Error("the download ran inside this module, where its go.mod and vendor/ decide things")
	}
	env := map[string]string{}
	for _, line := range strings.Split(stubLog(t, log, "env"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	for k, want := range map[string]string{"GOENV": "off", "GOTOOLCHAIN": "local", "GOWORK": "off",
		"GOSUMDB": "sum.golang.org"} {
		if env[k] != want {
			t.Errorf("the download ran with %s=%q, want %q", k, env[k], want)
		}
	}
	for _, k := range []string{"GONOSUMDB", "GOPRIVATE", "GOINSECURE", "GOFLAGS"} {
		if v, ok := env[k]; ok {
			t.Errorf("the download ran with %s=%q, which can skip the checksum database", k, v)
		}
	}

	for _, rel := range []string{"bin/go", "bin/gofmt", "pkg/tool/" + runtime.GOOS + "_" + runtime.GOARCH + "/compile",
		"lib/wasm/go_js_wasm_exec"} {
		if fi, err := os.Stat(filepath.Join(tc, rel)); err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("%s was not made executable: %v", rel, err)
		}
	}
	if fi, _ := os.Stat(filepath.Join(tc, "lib", "time", "zoneinfo.zip")); fi.Mode()&0o111 != 0 {
		t.Error("a data file under lib/ was made executable")
	}
}

func TestFetchToolchainKeepsAChecksumDatabaseItWasGiven(t *testing.T) {
	g, log := stubGo(t, map[string]string{"Dir": fakeToolchain(t, Toolchain), "Sum": "h1:fake="})
	custom := "sum.golang.org+033de0ae+Ac4zctda0e5eza+HJyk9SxEdh+s3Ux18htTTAD8OuAn8 https://proxy.example/sumdb"
	if _, err := fetchToolchain(g, append(os.Environ(), "GOSUMDB="+custom)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stubLog(t, log, "env"), "GOSUMDB="+custom+"\n") {
		t.Errorf("the download dropped GOSUMDB=%s", custom)
	}
}

func TestFetchToolchainRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		answer map[string]string
		want   string
	}{
		"a download error":   {map[string]string{"Error": "reading golang.org/toolchain: 404 Not Found"}, "404 Not Found"},
		"no checksum":        {map[string]string{"Dir": fakeToolchain(t, Toolchain)}, "no directory or checksum"},
		"another go version": {map[string]string{"Dir": fakeToolchain(t, "go1.26.6"), "Sum": "h1:x="}, "reports"},
	} {
		t.Run(name, func(t *testing.T) {
			g, _ := stubGo(t, tc.answer)
			if _, err := fetchToolchain(g, os.Environ()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("fetchToolchain = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

// A real run builds with the downloaded toolchain, never with the go on PATH that downloads it.
func TestARealRunBuildsWithTheDownloadedToolchain(t *testing.T) {
	tc := fakeToolchain(t, Toolchain)
	g, log := stubGo(t, map[string]string{"Dir": tc, "Sum": "h1:fake="})
	t.Setenv("PATH", filepath.Dir(g)+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := defaultDeps(t.TempDir(), os.Environ()).toolchain()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(tc, "bin", "go") {
		t.Errorf("a real run builds with %s, want the downloaded %s", got, filepath.Join(tc, "bin", "go"))
	}
	if !strings.Contains(stubLog(t, log, "args"), toolchainModule(runtime.GOOS, runtime.GOARCH)) {
		t.Error("the go on PATH was not asked to download the toolchain module")
	}
}
