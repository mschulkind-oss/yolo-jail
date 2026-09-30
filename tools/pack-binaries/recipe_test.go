package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	goversion "go/version"
)

// The toolchain constant has to be able to build this module at all: at or above go.mod's go
// line (BP-D9).
func TestToolchainIsAtOrAboveGoMod(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go ([0-9.]+)\s*$`).FindSubmatch(data)
	if m == nil {
		t.Fatal("go.mod has no go line")
	}
	if !goversion.IsValid(Toolchain) {
		t.Fatalf("Toolchain %q is not a Go version", Toolchain)
	}
	if goversion.Compare(Toolchain, "go"+string(m[1])) < 0 {
		t.Errorf("Toolchain %s is below go.mod's go %s, and could not build this module", Toolchain, m[1])
	}
	if want := "golang.org/toolchain@v0.0.1-" + Toolchain + ".darwin-arm64"; toolchainModule("darwin", "arm64") != want {
		t.Errorf("toolchainModule = %s, want %s", toolchainModule("darwin", "arm64"), want)
	}
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// Every variable that moves a byte is dropped, and every recipe setting is written out.
func TestBuildEnvIsTheRecipe(t *testing.T) {
	environ := []string{"HOME=/h", "PATH=/p", "GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/g",
		"GOTMPDIR=/t", "GOFLAGS=-ldflags=-X=main.stamp=x", "GOEXPERIMENT=nosomething",
		"GOROOT=/other/go", "GOFIPS140=latest", "GOAMD64=v3", "GOARM64=v9.0", "CGO_ENABLED=1",
		"CGO_CFLAGS=-O3", "GOTOOLCHAIN=go1.99.0", "GOWORK=/w/go.work", "GOENV=/e"}
	got := envMap(buildEnv(environ, "linux", "amd64"))
	for k, want := range map[string]string{"HOME": "/h", "PATH": "/p", "GOCACHE": "/c",
		"GOMODCACHE": "/m", "GOPATH": "/g", "GOTMPDIR": "/t", "GOENV": "off",
		"GOTOOLCHAIN": "local", "GOWORK": "off", "CGO_ENABLED": "0", "GOOS": "linux",
		"GOARCH": "amd64", "GOAMD64": "v1"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	for _, k := range []string{"GOFLAGS", "GOEXPERIMENT", "GOROOT", "GOFIPS140", "GOARM64", "CGO_CFLAGS"} {
		if v, ok := got[k]; ok {
			t.Errorf("%s=%q survived into the recipe", k, v)
		}
	}
	if got := envMap(buildEnv(environ, "darwin", "arm64")); got["GOARM64"] != "v8.0" || got["GOAMD64"] != "" {
		t.Errorf("arm64 levels: GOARM64=%q GOAMD64=%q", got["GOARM64"], got["GOAMD64"])
	}
	args := strings.Join(buildArgs("/out/toold", "toold"), " ")
	if args != "build -trimpath -buildvcs=false -mod=vendor -o /out/toold ./cmd/toold" {
		t.Errorf("buildArgs = %s", args)
	}
}

// §14.2's measurement as a test: two checkouts at different paths, with separate build caches
// and a polluted environment, build the same bytes — and the pollution would have moved them.
func TestTheRecipeBuildsTheSameBytesAnywhere(t *testing.T) {
	g := hostGo(t)
	a, b := fixtureCheckout(t), fixtureCheckout(t)
	polluted := append(os.Environ(), "GOFLAGS=-ldflags=-X=main.stamp=moved")
	var sums []string
	for _, root := range []string{a, b} {
		env := append(append([]string{}, polluted...), "GOCACHE="+t.TempDir())
		_, sum, err := buildOne(g, root, env, "toold", "linux/arm64", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		sums = append(sums, sum)
	}
	if sums[0] != sums[1] {
		t.Fatalf("the recipe built %s in one checkout and %s in the other", sums[0], sums[1])
	}

	// The control: the same build with the pollution let through is a different file.
	out := filepath.Join(t.TempDir(), "toold")
	cmd := exec.Command(g, buildArgs(out, "toold")...)
	cmd.Dir = a
	cmd.Env = append(buildEnv(os.Environ(), "linux", "arm64"), "GOFLAGS=-ldflags=-X=main.stamp=moved")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("control build: %v\n%s", err, b)
	}
	if sum, _ := fileSHA256(out); sum == sums[0] {
		t.Error("a stamped build has the recipe's digest, so this test cannot tell a scrubbed " +
			"environment from an unscrubbed one")
	}
}
