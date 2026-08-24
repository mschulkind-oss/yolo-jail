package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A pack's `mount` contribution and the config `mounts` key emit the SAME argv —
// `-v <host>:<dest>:ro` under /ctx — and Apple Container ignores the `:ro`. The
// config key has known that since ctxmounts_test.go; the pack kind did not, because
// the rule lived in a local variable inside the config loop rather than anywhere the
// second emitter could reach it.
//
// The consequence is not cosmetic. `mount` is origin-gated (packload.HonoredMounts)
// and a fetched pack's grant is approved by a human at `pack install` against the
// words "read-only". A backend that silently widens that to read-write makes the
// approval untrue, and the writes land in the user's real home.
func TestPackMountRefusedOnAppleContainerBecauseROIsIgnored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "datasets", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	pack := mountPackFixture(t, `{"kind":"mount","host":"datasets/acme","into":"acme"}`)

	// Control: podman honors it, read-only. If this half ever goes red the fix
	// below has over-reached into every backend.
	argv, _ := assembleMountPack(t, "podman", pack)
	want := filepath.Join(home, "datasets", "acme") + ":/ctx/acme:ro"
	if !containsArg(argv, want) {
		t.Fatalf("podman must mount the pack's dir read-only (%s); argv: %v", want, argv)
	}

	argv, printed := assembleMountPack(t, "container", pack)
	for _, a := range argv {
		if strings.Contains(a, "/ctx/acme") {
			t.Errorf("Apple Container emitted %q — a mount it would make WRITABLE, "+
				"from a grant the user approved as read-only", a)
		}
	}
	if !strings.Contains(printed, "read-only") || !strings.Contains(printed, "acme") {
		t.Errorf("the drop must say which pack and why; printed: %q", printed)
	}
}

// The FILE half of the same emitter fails differently and worse: ROFileMountArg's
// dereference produces a single-file bind, which Apple Container cannot do at all
// (apple/container#1089), so the mount was simply absent with nothing said. Skipping
// loudly is the whole fix here — there is no relocation available, because unlike a
// `reads-host` grant the reader is the AGENT, following a path the briefing names.
func TestPackMountFileSourceIsReportedNotSilentlyDroppedOnAppleContainer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "acme.toml"), []byte("x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := mountPackFixture(t, `{"kind":"mount","host":"acme.toml","into":"acme.toml"}`)

	argv, printed := assembleMountPack(t, "container", pack)
	for _, a := range argv {
		if strings.Contains(a, "/ctx/acme.toml") {
			t.Errorf("Apple Container cannot bind a single file, yet argv carries %q", a)
		}
	}
	if !strings.Contains(printed, "acme.toml") {
		t.Errorf("a mount that cannot arrive must be reported; printed: %q", printed)
	}
}

// mountPackFixture loads a one-contribution local pack that may read the host.
func mountPackFixture(t *testing.T, contribution string) []*packload.Pack {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "pack.json"),
		[]byte(`{"contributes":[`+contribution+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme", true)
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	p.MayAccessHost = true
	return []*packload.Pack{p}
}

// assembleMountPack runs just the emitter under test, so the assertion is about the
// mount decision and not about everything else a full assemble emits.
func assembleMountPack(t *testing.T, rt string, packs []*packload.Pack) ([]string, string) {
	t.Helper()
	o := goldenOptions("/ws", os.Getenv("HOME"))
	var sb strings.Builder
	o.Stdout = &sb
	args := o.hostMountArgs(&assembleInput{
		rt:           rt,
		packs:        packs,
		wsState:      t.TempDir(),
		mountTargets: map[string]struct{}{},
	})
	return args, sb.String()
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
