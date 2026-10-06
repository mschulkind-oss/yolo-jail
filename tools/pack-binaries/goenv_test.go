package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goEnvAnswer makes the stub go's `go env -json` print answer.
func goEnvAnswer(t *testing.T, log string, answer map[string]string) {
	t.Helper()
	body, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(log, "goenv"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func envOf(t *testing.T, log, name string) map[string]string {
	t.Helper()
	env := map[string]string{}
	for _, line := range strings.Split(stubLog(t, log, name), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

// A GOPROXY or GOSUMDB the user set with `go env -w` lives in the go env file, which the download
// runs with GOENV=off to keep out every setting that could skip the checksum database. So the
// download asks the go on PATH for the effective two first, and passes them explicitly (BP-D27):
// a user behind goproxy.cn, or a corporate proxy, downloads through it. GOSUMDB=off is still
// overridden, and GONOSUMDB, GOPRIVATE, GOINSECURE and GOFLAGS are still dropped.
//
// Call-site check: drop effectiveProxy's answer from downloadEnv's call and this fails.
func TestFetchToolchainUsesTheProxyTheGoEnvFileNames(t *testing.T) {
	for name, tc := range map[string]struct {
		goenv        map[string]string
		proxy, sumdb string
	}{
		"a proxy and database": {map[string]string{"GOPROXY": "https://goproxy.cn,direct",
			"GOSUMDB": "sum.golang.google.cn"}, "https://goproxy.cn,direct", "sum.golang.google.cn"},
		"a database switched off": {map[string]string{"GOPROXY": "https://corp.example/go",
			"GOSUMDB": "off"}, "https://corp.example/go", "sum.golang.org"},
	} {
		t.Run(name, func(t *testing.T) {
			g, log := stubGo(t, map[string]string{"Dir": fakeToolchain(t, Toolchain), "Sum": "h1:fake="})
			goEnvAnswer(t, log, tc.goenv)
			environ := append(os.Environ(), "GOENV=/home/someone/.config/go/env", "GOFLAGS=-mod=mod",
				"GONOSUMDB=golang.org", "GOTOOLCHAIN=go1.99.0")
			if _, err := fetchToolchain(g, environ); err != nil {
				t.Fatal(err)
			}
			env := envOf(t, log, "env")
			for k, want := range map[string]string{"GOPROXY": tc.proxy, "GOSUMDB": tc.sumdb, "GOENV": "off"} {
				if env[k] != want {
					t.Errorf("the download ran with %s=%q, want %q", k, env[k], want)
				}
			}
			for _, k := range []string{"GONOSUMDB", "GOFLAGS"} {
				if v, ok := env[k]; ok {
					t.Errorf("the download ran with %s=%q", k, v)
				}
			}
			// The question reads the user's env file, never switches toolchains to answer it,
			// and carries no GOFLAGS that could fail it.
			q := envOf(t, log, "envenv")
			if q["GOENV"] != "/home/someone/.config/go/env" || q["GOTOOLCHAIN"] != "local" || q["GOFLAGS"] != "" {
				t.Errorf("`go env` ran with GOENV=%q GOTOOLCHAIN=%q GOFLAGS=%q", q["GOENV"],
					q["GOTOOLCHAIN"], q["GOFLAGS"])
			}
			if got := strings.Fields(stubLog(t, log, "envargs")); strings.Join(got, " ") != "env -json GOPROXY GOSUMDB" {
				t.Errorf("the question was `go %v`", got)
			}
		})
	}
}
