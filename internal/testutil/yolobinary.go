package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// buildYolo links the real hidden-command dispatcher into dir. A variable so the sharing test
// can count builds without linking a binary.
var buildYolo = func(dir string) ([]byte, error) {
	_, source, _, _ := runtime.Caller(0)
	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "yolo"), "./cmd/yolo")
	cmd.Dir = filepath.Join(filepath.Dir(source), "../..")
	return cmd.CombinedOutput()
}

var shared struct {
	mu  sync.Mutex
	on  bool
	dir string
}

// ShareYoloBinary makes every YoloBinaryDir call in this process return ONE build, and returns
// the function that removes it. A TestMain calls it and calls the release before exiting.
//
// WHY: Go does not cache a link, so a package whose tests call YoloBinaryDir hundreds of times
// (internal/entrypoint: every launcher test and subtest) spent most of its run relinking the
// same binary, and hit the 20 minute package timeout on a slower macOS runner (CI run
// 46076668560) with no test hung. Callers only read the directory, so one build is the same
// fixture. A package that does not opt in keeps one build per call and a t.TempDir that removes
// itself: a shared directory needs an owner that removes it.
func ShareYoloBinary() (release func()) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	shared.on = true
	return func() {
		shared.mu.Lock()
		defer shared.mu.Unlock()
		shared.on = false
		if shared.dir != "" {
			os.RemoveAll(shared.dir)
			shared.dir = ""
		}
	}
}

// YoloBinarySharing reports whether this process opted in with ShareYoloBinary.
func YoloBinarySharing() bool {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	return shared.on
}

// YoloBinaryDir builds the real hidden-command dispatcher for generated-shell tests.
// It uses Go's shared default build cache, never a workspace-local cache.
func YoloBinaryDir(t *testing.T) string {
	t.Helper()
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.on && shared.dir != "" {
		return shared.dir
	}
	dir := ""
	if shared.on {
		d, err := os.MkdirTemp("", "yolo-test-bin-")
		if err != nil {
			t.Fatal(err)
		}
		dir = d
	} else {
		dir = t.TempDir()
	}
	if out, err := buildYolo(dir); err != nil {
		if shared.on {
			os.RemoveAll(dir)
		}
		t.Fatalf("build yolo: %v\n%s", err, out)
	}
	if shared.on {
		shared.dir = dir
	}
	return dir
}
