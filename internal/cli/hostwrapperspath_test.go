package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// pointWrappersAt makes the wrappers stage name yolo, for the test's duration.
func pointWrappersAt(t *testing.T, yolo string) {
	t.Helper()
	prev := hostWrapperYolo
	hostWrapperYolo = func() string { return yolo }
	t.Cleanup(func() { hostWrapperYolo = prev })
}

// TestHostApplyWrappersNameTheApplyingYoloByPath pins the CALL SITE of the absolute-path wrapper:
// the wrappers stage of a real host apply hands hostwrap the yolo hostWrapperYolo names, so the
// wrapper it writes execs that file by path. hostwrap's own tests prove a wrapper so written
// starts from a PATH without yolo; this fails when hostapply.go stops passing the path, whatever
// it passes instead.
func TestHostApplyWrappersNameTheApplyingYoloByPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	stubDeclaredBins(t)
	yolo := filepath.Join(t.TempDir(), "opt", "bin", "yolo")
	pointWrappersAt(t, yolo)

	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	wrapper := filepath.Join(paths.WrapDirUnder(home), "claude")
	body, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatalf("no wrapper generated: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errw.String())
	}
	if string(body) != hostwrap.BodyFor(yolo, "claude") {
		t.Errorf("the wrapper does not exec the applying yolo by path:\n%s", body)
	}
	if named, ok := hostwrap.NamedYolo(string(body), "claude"); !ok || named != yolo {
		t.Errorf("the wrapper names %q (%v), want %q", named, ok, yolo)
	}
}

// TestHostApplyDryRunPlansAgainstTheApplyingYolo: an observing apply plans with the same yolo a
// writing one writes, so a wrapper naming another file is reported as one it would rewrite, and
// the dry run and the launch gate's survey see the change the next --assert makes.
func TestHostApplyDryRunPlansAgainstTheApplyingYolo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	stubDeclaredBins(t)
	dir := paths.WrapDirUnder(home)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A wrapper an older yolo wrote: it finds yolo through PATH.
	legacy := hostwrap.BodyFor("yolo", "claude")
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	pointWrappersAt(t, filepath.Join(t.TempDir(), "yolo"))

	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, false /* observe */, strings.NewReader(""))
	if !strings.Contains(out.String(), "would write 1 wrapper(s) (~1)") {
		t.Errorf("an observing apply did not plan the rewrite of a bare-yolo wrapper:\n%s", out.String())
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "claude")); string(got) != legacy {
		t.Errorf("an observing apply wrote the wrapper:\n%s", got)
	}
}
