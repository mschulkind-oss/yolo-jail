package cli

// `yolo internal config-dump` is the differential-testing oracle: it prints the
// merged config plus the validation errors/warnings, and its exit code is the
// verdict. It was the only ValidateConfig caller passing a nil LoopholeResolver,
// which was harmless while nothing keyed off "is this loophole installed" — and
// stopped being harmless when the §4.3b enable-uninstalled rule shipped, because a
// nil resolver makes the known set EMPTY and every name read as uninstalled. So the
// oracle reported a fatal, and exited 1, for a config `yolo check` and the launch
// path both accept.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of body and returns what was
// written. runConfigDump prints with fmt.Println, so this is the only seam.
func captureStdout(t *testing.T, body func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- sb.String()
	}()
	body()
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// A workspace enabling a loophole that IS installed on this machine (host-processes
// is bundled, hence always resolvable) must dump clean and exit 0 — the same verdict
// `yolo check` gives it.
func TestConfigDumpResolvesInstalledLoopholes(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"loopholes": {"host-processes": {"enabled": true}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var rc int
	out := captureStdout(t, func() { rc = runConfigDump([]string{ws}) })
	if rc != 0 {
		t.Errorf("config-dump rc = %d, want 0 for a config the launch path accepts:\n%s", rc, out)
	}
	if strings.Contains(out, "not installed on this machine") {
		t.Errorf("a bundled loophole was reported as uninstalled:\n%s", out)
	}
}
