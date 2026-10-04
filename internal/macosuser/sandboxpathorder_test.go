package macosuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// sandboxpathorder_test.go pins SandboxPath's ORDER against BootPath's, which is the
// authority every backend's PATH follows (AGENTS.md, "PATH order"). It was a third,
// hand-written copy and had drifted: ~/.local/bin sat third, ahead of the npm prefix, the mise
// shims and $GOPATH/bin, where BootPath puts it sixth.

// The HEAD of the sandbox PATH is BootPath's head for the same home, entry by entry: every
// entry BootPath puts before its own platform tail (the store farm, then /bin and /usr/bin).
func TestTheSandboxPathHeadIsBootPathsHead(t *testing.T) {
	home := "/Users/_yolojail"
	boot := strings.Split(entrypoint.BootPath(entrypoint.NewEnv(map[string]string{
		"JAIL_HOME":     home,
		"MISE_DATA_DIR": SandboxMiseData(home),
	})), ":")
	tail := []string{entrypoint.StorePackagesBin(), "/bin", "/usr/bin"}
	if len(boot) <= len(tail) || strings.Join(boot[len(boot)-len(tail):], ":") != strings.Join(tail, ":") {
		t.Fatalf("premise: BootPath no longer ends in %v, so its head cannot be read off it: %v", tail, boot)
	}
	head := boot[:len(boot)-len(tail)]

	store := "/nix/store/abc-env/bin"
	got := strings.Split(SandboxPath(home, []string{store}), ":")
	if len(got) < len(head) {
		t.Fatalf("SandboxPath has %d entries, fewer than BootPath's %d-entry head: %v", len(got), len(head), got)
	}
	for i, want := range head {
		if got[i] != want {
			t.Errorf("SandboxPath[%d] = %q, BootPath's head has %q there\n sandbox %v\n    boot %v",
				i, got[i], want, got, boot)
		}
	}
	// THE TAIL IS macOS's OWN, kept by decision: the darwin store prefix, the staged yolo's
	// directory, then the system dirs in /etc/paths' order.
	wantTail := []string{store, filepath.Dir(StagedYoloPath("")), "/usr/bin", "/bin", "/usr/sbin", "/sbin"}
	if gotTail := got[len(head):]; strings.Join(gotTail, ":") != strings.Join(wantTail, ":") {
		t.Errorf("SandboxPath's platform tail = %v, want %v", gotTail, wantTail)
	}
}

// THE BEHAVIOR THE ORDER DECIDES: a name both a mise tool and something in ~/.local/bin
// provide resolves to the mise shim, as it does in every container. Red on the old order,
// which ranked ~/.local/bin third and so ran whatever a vendor installer or pipx left there.
func TestAMiseShimOutranksALocalBinToolOfTheSameName(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(SandboxMiseData(home), "shims", "ruff")
	local := filepath.Join(home, ".local", "bin", "ruff")
	for _, p := range []string{shim, local} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := firstOnPath(SandboxPath(home, nil), "ruff"); got != shim {
		t.Errorf("`ruff` resolves to %q on the sandbox PATH, want the mise shim %q", got, shim)
	}
}

// firstOnPath is the first executable regular file named name along path, or "".
func firstOnPath(path, name string) string {
	for _, dir := range strings.Split(path, ":") {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
