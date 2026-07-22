package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withBuildDir points GLOBAL_STORAGE at a temp HOME so BuildDir()/GlobalCache()
// resolve under it, returning the build dir.
func withBuildDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bd := filepath.Join(home, ".local", "share", "yolo-jail", "build")
	if err := os.MkdirAll(bd, 0o755); err != nil {
		t.Fatal(err)
	}
	return bd
}

func TestAutoLoadImageFreshLoad(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	loaded := false
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		Getpid:  func() int { return 4242 },
		BuildStorePath: func(repoRoot string, extra []any, outLink string) (string, []string) {
			return "/nix/store/abc-image", nil
		},
		Run: func(argv []string) (int, bool) {
			// image inspect: first call before load → not present (rc 1);
			// load → success (rc 0).
			if len(argv) >= 2 && argv[1] == "load" {
				loaded = true
				return 0, true
			}
			return 1, true // inspect: not present
		},
		Materialize: func(storePath, cacheFile string) int64 {
			// Simulate a materialized cache file.
			_ = os.WriteFile(cacheFile, []byte("tar"), 0o644)
			return 12 * 1024 * 1024
		},
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; out=%q", out.String())
	}
	if !loaded {
		t.Error("expected a load command to run")
	}
	if !strings.Contains(out.String(), "first run") {
		t.Errorf("expected first-run message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Done: loaded image") {
		t.Errorf("expected done message, got %q", out.String())
	}
}

func TestAutoLoadImageAlreadyLoaded(t *testing.T) {
	bd := withBuildDir(t)
	// Sentinel already lists the store path AND image is present → no reload.
	storePath := "/nix/store/xyz-image"
	sentinel := filepath.Join(bd, "last-load-podman")
	if err := os.WriteFile(sentinel, []byte(storePath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	materialized := false
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return storePath, nil
		},
		Run: func(argv []string) (int, bool) { return 0, true }, // inspect present
		Materialize: func(string, string) int64 {
			materialized = true
			return 1
		},
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; out=%q", out.String())
	}
	if materialized {
		t.Error("should not materialize when already loaded + present")
	}
	if strings.Contains(out.String(), "Image load needed") {
		t.Errorf("unexpected load-needed message: %q", out.String())
	}
}

func TestAutoLoadImageBuildFailsUsesExisting(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"boom"}
		},
		Run: func(argv []string) (int, bool) { return 0, true }, // inspect present
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; want true (existing image)")
	}
	if !strings.Contains(out.String(), "Using existing") {
		t.Errorf("expected using-existing message, got %q", out.String())
	}
}

// TestAutoLoadOffloadInvokedOnMacOS: when the plain build fails on macOS, the
// container-builder offload (J3) is tried; its success feeds the normal load
// path. On Linux the offload must NOT be consulted.
func TestAutoLoadOffloadInvokedOnMacOS(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	offloadCalled := false
	opts := AutoLoadOptions{
		Runtime: "podman",
		IsMacOS: true,
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"needs linux"} // plain build fails
		},
		BuildOffload: func(string, []any, string) (string, []string) {
			offloadCalled = true
			return "/nix/store/offloaded", nil // offload succeeds
		},
		Run: func(argv []string) (int, bool) {
			if len(argv) >= 2 && argv[1] == "load" {
				return 0, true // load succeeds
			}
			return 1, true // inspect: not present
		},
		Materialize: func(storePath, cacheFile string) int64 {
			_ = os.WriteFile(cacheFile, []byte("tar"), 0o644)
			return 12 * 1024 * 1024
		},
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; want true (offload built the image)\n%s", out.String())
	}
	if !offloadCalled {
		t.Error("BuildOffload was not invoked on a macOS build failure")
	}
}

func TestAutoLoadOffloadSkippedOnLinux(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	offloadCalled := false
	opts := AutoLoadOptions{
		Runtime: "podman",
		IsMacOS: false, // Linux
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"boom"}
		},
		BuildOffload: func(string, []any, string) (string, []string) {
			offloadCalled = true
			return "/nix/store/x", nil
		},
		Run:             func(argv []string) (int, bool) { return 1, true },
		DiagnoseFailure: func([]string) (string, string) { return "t", "r" },
	}
	if AutoLoadImage(opts) {
		t.Fatal("AutoLoadImage = true; want false on Linux (no offload)")
	}
	if offloadCalled {
		t.Error("BuildOffload must NOT be invoked on Linux")
	}
}

func TestAutoLoadImageBuildFailsNoImage(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	opts := AutoLoadOptions{
		Runtime: "podman",
		Out:     &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			return "", []string{"nix: dependency failed"}
		},
		Run: func(argv []string) (int, bool) { return 1, true }, // inspect: not present
		DiagnoseFailure: func(tail []string) (string, string) {
			return "needs a Linux builder", "do the thing"
		},
	}
	if AutoLoadImage(opts) {
		t.Fatal("AutoLoadImage = true; want false (no image, can't build)")
	}
	s := out.String()
	if !strings.Contains(s, "Cannot start jail: needs a Linux builder.") {
		t.Errorf("missing diagnosis title: %q", s)
	}
	if !strings.Contains(s, "do the thing") {
		t.Errorf("missing remedy: %q", s)
	}
}

// TestAutoLoadSkipBuildUsesExisting is the D2 (graceful launch degradation)
// regression: when repo-root resolution fails, the run slice sets SkipBuild so
// AutoLoadImage never shells out to `nix build` in an empty cwd (which would
// error or, worse, build against the process's own cwd). With an image already
// present in the runtime it must proceed on it — never invoking BuildStorePath.
func TestAutoLoadSkipBuildUsesExisting(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	buildCalled := false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "/nix/store/should-not-happen", nil
		},
		Run: func(argv []string) (int, bool) { return 0, true }, // inspect present
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; want true (existing image on degraded launch)\n%s", out.String())
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !strings.Contains(out.String(), "Using existing") {
		t.Errorf("expected using-existing message, got %q", out.String())
	}
}

// TestAutoLoadSkipBuildLoadsCachedTar: degraded launch with no runtime image
// but a cached tar present must load the cache — again without building.
func TestAutoLoadSkipBuildLoadsCachedTar(t *testing.T) {
	bd := withBuildDir(t)
	// Drop a cached tar into GlobalCache()/images (sibling of the build dir).
	cacheImages := filepath.Join(filepath.Dir(bd), "cache", "images")
	if err := os.MkdirAll(cacheImages, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheImages, "jail.tar"), []byte("tar"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	buildCalled, loaded := false, false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "", nil
		},
		Run: func(argv []string) (int, bool) {
			if len(argv) >= 2 && argv[1] == "load" {
				loaded = true
				return 0, true
			}
			return 1, true // inspect: not present
		},
	}
	if !AutoLoadImage(opts) {
		t.Fatalf("AutoLoadImage = false; want true (cached tar on degraded launch)\n%s", out.String())
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !loaded {
		t.Error("expected a load command for the cached tar")
	}
}

// TestAutoLoadSkipBuildNoImageFails: degraded launch with neither a runtime
// image nor a cached tar can't conjure one — must fail with the degraded
// diagnosis (not a nix-build diagnosis, since no build was attempted).
func TestAutoLoadSkipBuildNoImageFails(t *testing.T) {
	withBuildDir(t)
	var out bytes.Buffer
	buildCalled := false
	opts := AutoLoadOptions{
		Runtime:   "podman",
		SkipBuild: true,
		Out:       &out,
		BuildStorePath: func(string, []any, string) (string, []string) {
			buildCalled = true
			return "", nil
		},
		Run: func(argv []string) (int, bool) { return 1, true }, // inspect: not present
	}
	if AutoLoadImage(opts) {
		t.Fatal("AutoLoadImage = true; want false (degraded, no image available)")
	}
	if buildCalled {
		t.Error("BuildStorePath must NOT run when SkipBuild is set")
	}
	if !strings.Contains(out.String(), "Cannot start jail") {
		t.Errorf("expected a cannot-start diagnosis, got %q", out.String())
	}
}
