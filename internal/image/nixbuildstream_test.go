package image

import (
	"github.com/mschulkind-oss/yolo-jail/internal/nixstderr"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestANixLineLongerThanTheScannerReadsDoesNotWedgeTheBuild: runNixBuild reads nix's stderr a line
// at a time, up to 1 MiB a line. A longer line ended the read loop, and the Wait after it then
// waited on a nix blocked writing into a pipe nobody read any more — a launch hung for good, with
// nothing printed. The stand-in prints a 2 MiB line, then a last line, and exits 0.
func TestANixLineLongerThanTheScannerReadsDoesNotWedgeTheBuild(t *testing.T) {
	freshNixChildren(t)
	out := filepath.Join(t.TempDir(), "out")
	script := `head -c 2097152 /dev/zero | tr '\0' x >&2; echo >&2; echo "after the long line" >&2; ln -s /nix/store/fake-out ` + shquote.Quote(out)
	type built struct {
		path string
		tail []string
	}
	done := make(chan built, 1)
	go func() {
		p, tail := runNixBuild([]string{"sh", "-c", script}, t.TempDir(), os.Environ(), out, io.Discard)
		done <- built{p, tail}
	}()
	select {
	case b := <-done:
		if b.path == "" {
			t.Errorf("a nix that exited 0 was reported as a failed build: %q", b.tail)
		}
		joined := strings.Join(b.tail, "\n")
		if !strings.Contains(joined, "nix printed a line longer than") {
			t.Errorf("the tail does not say a line was too long to keep (%d bytes)", len(joined))
		}
		if !strings.HasSuffix(joined, "after the long line") {
			t.Errorf("the lines after the long one were not read: the tail ends %q",
				joined[max(0, len(joined)-200):])
		}
		if len(joined) > nixstderr.MaxLine+4096 {
			t.Errorf("the tail kept %d bytes of a 2 MiB line, more than the %d-byte cap", len(joined), nixstderr.MaxLine)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runNixBuild never returned from a nix that printed a line over 1 MiB and exited: " +
			"the read loop stopped at the long line and Wait waits on a nix blocked on its full pipe")
	}
}

// TestACheckBuildOfALongNixLineDoesNotWedge is the same hang in `yolo check`'s image build
// (BuildOCIImage), which read nix's stderr with its own 1 MiB bufio.Scanner and then waited on
// nix. The stand-in nix first on PATH prints a 2 MiB line, then a last line, makes the out-link it
// was handed and exits 0.
func TestACheckBuildOfALongNixLineDoesNotWedge(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"link=\n" +
		"while [ $# -gt 0 ]; do if [ \"$1\" = --out-link ]; then link=$2; fi; shift; done\n" +
		"head -c 2097152 /dev/zero | tr '\\0' x >&2; echo >&2\n" +
		"echo 'after the long line' >&2\n" +
		"ln -s /nix/store/fake-check-image \"$link\"\n"
	if err := os.WriteFile(filepath.Join(bin, "nix"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	type built struct {
		path string
		tail []string
	}
	done := make(chan built, 1)
	go func() {
		p, tail := BuildOCIImage(OCIBuildRequest{RepoRoot: t.TempDir()})
		done <- built{p, tail}
	}()
	select {
	case b := <-done:
		if b.path != "/nix/store/fake-check-image" {
			t.Errorf("a nix that exited 0 was reported as %q, want its out-link's target: %q", b.path, b.tail)
		}
		joined := strings.Join(b.tail, "\n")
		if !strings.Contains(joined, "nix printed a line longer than") {
			t.Errorf("the tail does not say a line was too long to keep (%d bytes)", len(joined))
		}
		if !strings.HasSuffix(joined, "after the long line") {
			t.Errorf("the lines after the long one were not read: the tail ends %q",
				joined[max(0, len(joined)-200):])
		}
		if len(joined) > nixstderr.MaxLine+4096 {
			t.Errorf("the tail kept %d bytes of a 2 MiB line, more than the %d-byte cap", len(joined), nixstderr.MaxLine)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("BuildOCIImage never returned from a nix that printed a line over 1 MiB and exited: " +
			"the read loop stopped at the long line and Wait waits on a nix blocked on its full pipe")
	}
}
