package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contextmounts_test.go drives docs/design/context-mounts.md §4 steps 1-2 through a real
// jail: a read-write `mounts` element from the USER config is writable from inside and its
// bytes reach the host, a read-only string element beside it stays unwritable, every launch
// names the read-write one, and the jail is told its context dir. The unit suite pins each
// half at its call site (internal/cli/run/ctxmountsrw_test.go, contextdir_test.go); this is
// the one place the bind itself is exercised — podman accepting `-v src:dest` with no mode
// and the kernel enforcing `:ro` beside it.
//
// ⚠ A NESTED JAIL IS ROOTFUL BY CONSTRUCTION (`--userns host`), so the ownership cells of
// §2.5 are not what this measures: here in-jail root is the outer jail's root. What a
// rootless host does with a non-root writer is CI's to show.
func TestAReadWriteMountCrossesBothWaysAndIsNamedAtLaunch(t *testing.T) {
	requireJail(t)

	rw := resolvedTempDir(t)
	ro := resolvedTempDir(t)
	if err := os.WriteFile(filepath.Join(rw, "from-host.txt"), []byte("host-bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ro, "ro.txt"), []byte("ro-bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// USER SCOPE: a read-write element anywhere else is a `yolo check` error and a refused
	// launch (config.rwMountTrusted), which the unit suite pins.
	packHome(t, `{"mounts": [
	  {"host": "`+rw+`", "at": "/ctx/rwdata", "mode": "rw"},
	  "`+ro+`:/ctx/rodata"
	]}`)
	dir := writeProject(t, `{"network": {"mode": "bridge"}}`)

	// Every marker is ASSEMBLED by the shell, so none appears literally in the script: the
	// launch echoes the command it executes, and a literal marker would match that echo.
	r := runYolo(t, dir, strings.Join([]string{
		`echo "CTX=$YOLO_CONTEXT_DIR"`,
		`cat /ctx/rwdata/from-host.txt`,
		`echo "RW-$(echo jail-bytes > /ctx/rwdata/from-jail.txt && echo WRITE-OK || echo WRITE-FAILED)"`,
		`cat /ctx/rodata/ro.txt`,
		`echo "RO-$(touch /ctx/rodata/nope 2>/dev/null && echo WRITABLE || echo REFUSED)"`,
	}, "; "))
	if r.rc != 0 {
		t.Fatalf("rc=%d\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	out := r.combined()
	for _, want := range []string{"CTX=/ctx\n", "host-bytes", "RW-WRITE-OK", "ro-bytes", "RO-REFUSED"} {
		if !strings.Contains(out, want) {
			t.Errorf("the jail's output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "RO-WRITABLE") {
		t.Errorf("a read-only string mount was writable from inside the jail:\n%s", out)
	}
	// THE DISCLOSURE, on the launch stream (§2.4).
	if !strings.Contains(r.stderr, "Read-write mount:") || !strings.Contains(r.stderr, rw+" → /ctx/rwdata") {
		t.Errorf("the launch did not name the read-write mount:\n%s", r.stderr)
	}
	// AND THE HOST SEES WHAT THE JAIL WROTE, which is the whole of what the mode means.
	body, err := os.ReadFile(filepath.Join(rw, "from-jail.txt"))
	if err != nil {
		t.Fatalf("the jail's write did not reach the host source: %v", err)
	}
	if string(body) != "jail-bytes\n" {
		t.Errorf("the host reads %q, not what the jail wrote", body)
	}
}

// The launch refuses a read-write element in the WORKSPACE config, which is agent-editable
// (context-mounts.md §2.2, OQ-WT1's rule D), before any container starts.
func TestAWorkspaceReadWriteMountRefusesTheLaunch(t *testing.T) {
	requireJail(t)

	rw := resolvedTempDir(t)
	packHome(t, `{}`)
	dir := writeProject(t, `{"mounts": [{"host": "`+rw+`", "mode": "rw"}]}`)

	r := runYolo(t, dir, `echo SHOULD-NOT-RUN`)
	if r.rc == 0 || strings.Contains(r.combined(), "SHOULD-NOT-RUN") {
		t.Fatalf("a workspace-scope read-write mount launched a jail (rc=%d):\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "user-scope only") {
		t.Errorf("the refusal does not say the mount is user-scope only:\n%s", r.combined())
	}
}
