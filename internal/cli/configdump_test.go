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

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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

// A workspace enabling a loophole that IS installed on this machine must dump clean
// and exit 0 — the same verdict `yolo check` gives it.
//
// "Installed" USED TO MEAN "bundled, hence unconditionally resolvable", and this test
// named `host-processes` for exactly that reason. It stopped being true on 2026-08-18:
// that loophole ships in an official PACK now, so it resolves only for a config that
// SELECTS the pack — which is what the user config below does, and what makes this the
// stronger version of the same assertion. The lazy pack-module resolver
// (internal/cli/run's init) is the thing under test: without it, selecting the pack and
// enabling its loophole would still warn "not installed on this machine" and exit 1.
func TestConfigDumpResolvesInstalledLoopholes(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The record is process-wide by design (it IS the convergence point), so a
	// recording leaked in from another test would decide this one.
	loopholes.ResetPackModules()
	t.Cleanup(loopholes.ResetPackModules)

	userCfg := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userCfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userCfg, "config.jsonc"),
		[]byte(`{"packs": ["host-processes"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("a SELECTED pack's loophole was reported as uninstalled:\n%s", out)
	}
}
