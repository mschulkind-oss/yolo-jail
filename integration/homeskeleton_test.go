package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestPodmanHomeIsAPerJailReadOnlySkeleton asserts, from INSIDE a real container, what the
// per-jail home skeleton promises (docs/design/base-home-legacy-state.md#8-build-order-and-done-conditions):
//
//   - /home/agent is a read-only bind of THIS jail's skeleton under
//     <state>/agents/<cname>/home/, not of the machine store <state>/home every podman jail
//     used to share;
//   - writing an undeclared home path still fails with EROFS;
//   - a pack the jail did not select leaves no trace in its home: a claude-only jail has no
//     ~/.codex, ~/.copilot or ~/.pi, and a codex-only jail has no
//     ~/.claude-shared-credentials (the machine's Claude refresh token lived there, readable
//     through the shared base) and its ~/.claude.json redirect dangles;
//   - a `writable_home_dirs` entry is still writable: its rw bind nests inside the :ro
//     skeleton, on a mountpoint the HOST created, which is the mount most exposed to rootless
//     ID mapping.
//
// WHY AN INTEGRATION TEST: the unit suite builds the skeleton and checks the argv against it,
// but only a container shows what the kernel mounted. And CI runs this package on ROOTLESS
// podman, which is the one check of the skeleton's ownership under ID mapping that a nested
// jail cannot give — a nested jail forces `--userns=host` (AGENTS.md, the Testing
// carve-outs). The STAT lines printed below are what to compare against a launch made before
// the skeleton, on the same host.
func TestPodmanHomeIsAPerJailReadOnlySkeleton(t *testing.T) {
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("the home skeleton is podman's; runtime here is %q (Apple Container binds the "+
			"workspace overlay whole at /home/agent)", rt)
	}

	const probe = `
awk '$5 == "/home/agent" {print "HOMEMOUNT " $4 " " $6}' /proc/self/mountinfo
if touch "$HOME/.skeleton-probe" 2>/tmp/skeleton-touch.err; then
  echo "TOUCH ok"
else
  echo "TOUCH failed: $(cat /tmp/skeleton-touch.err)"
fi
for p in .codex .copilot .pi .claude .claude-shared-credentials .gemini-shared-credentials; do
  if [ -e "$HOME/$p" ] || [ -L "$HOME/$p" ]; then echo "PRESENT $p"; else echo "ABSENT $p"; fi
done
if [ -L "$HOME/.claude.json" ]; then echo "LINK .claude.json"; fi
if [ -e "$HOME/.claude.json" ]; then echo "RESOLVES .claude.json"; else echo "DANGLES .claude.json"; fi
stat -c 'STAT %n %u:%g %a' /home/agent /home/agent/.config
if [ -d "$HOME/.skel-rw" ]; then
  if touch "$HOME/.skel-rw/ok" 2>/tmp/skeleton-rw.err; then
    echo "RWTOUCH ok"
  else
    echo "RWTOUCH failed: $(cat /tmp/skeleton-rw.err)"
  fi
  stat -c 'STAT %n %u:%g %a' /home/agent/.skel-rw
fi
`

	// launch runs the probe in a fresh jail selecting packsJSON, with wsConfig as the
	// workspace config, and returns its lines, after checking the two properties every
	// podman jail must have.
	launch := func(t *testing.T, packsJSON, wsConfig string) (dir string, lines map[string]bool, out string) {
		t.Helper()
		dir = writeProject(t, wsConfig)
		packHome(t, `{"packs": `+packsJSON+`}`)
		r := runYolo(t, dir, probe)
		if r.rc != 0 {
			t.Fatalf("launch failed: rc %d\n%s", r.rc, r.combined())
		}
		lines = map[string]bool{}
		for _, l := range strings.Split(r.stdout, "\n") {
			lines[strings.TrimSpace(l)] = true
		}
		t.Logf("probe output:\n%s", r.stdout)

		cname := naming.FromWorkspace(dir)
		var mount string
		for l := range lines {
			if strings.HasPrefix(l, "HOMEMOUNT ") {
				mount = l
			}
		}
		if mount == "" {
			t.Fatalf("no /home/agent mount in the jail's mountinfo:\n%s", r.stdout)
		}
		fields := strings.Fields(mount)
		root, opts := fields[1], fields[len(fields)-1]
		if want := "yolo-jail/agents/" + cname + "/home/"; !strings.Contains(root, want) {
			t.Errorf("/home/agent is bound from %q, want this jail's skeleton under .../%s — "+
				"a bind of <state>/home is the shared base every podman jail used to see", root, want)
		}
		if strings.HasSuffix(root, "/yolo-jail/home") {
			t.Errorf("/home/agent is still the machine store %q", root)
		}
		if !strings.Contains(","+opts+",", ",ro,") {
			t.Errorf("/home/agent is mounted %q, want read-only", opts)
		}
		// The host side: the skeleton the jail shows is one this launch built, under the
		// root the reaper removes with the jail's AGENTS_DIR entry.
		skelRoot := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "agents", cname, "home")
		entries, err := os.ReadDir(skelRoot)
		if err != nil || len(entries) == 0 {
			t.Errorf("no skeleton on the host under %s: %v", skelRoot, err)
		} else {
			found := false
			for _, e := range entries {
				if strings.HasSuffix(root, "/"+e.Name()) {
					found = true
				}
			}
			if !found {
				t.Errorf("the jail's home root %q is none of the host's skeletons under %s", root, skelRoot)
			}
		}

		touched := false
		for l := range lines {
			if strings.HasPrefix(l, "TOUCH failed:") && strings.Contains(l, "Read-only file system") {
				touched = true
			}
		}
		if !touched {
			t.Errorf("writing an undeclared home path did not fail with EROFS:\n%s", r.stdout)
		}
		return dir, lines, r.stdout
	}

	t.Run("claude only", func(t *testing.T) {
		_, lines, out := launch(t, `["claude"]`, `{}`)
		for _, p := range []string{".claude", ".claude-shared-credentials"} {
			if !lines["PRESENT "+p] {
				t.Errorf("the selected claude pack's ~/%s is missing:\n%s", p, out)
			}
		}
		for _, p := range []string{".codex", ".copilot", ".pi", ".gemini-shared-credentials"} {
			if !lines["ABSENT "+p] {
				t.Errorf("a claude-only jail has ~/%s, which no selected pack declares:\n%s", p, out)
			}
		}
	})

	t.Run("codex only", func(t *testing.T) {
		// The credential preflight refuses a launch that cannot deliver a cataloged
		// provider's key, selected or not — codex_selection_test.go sets it for the same
		// reason. A placeholder; nothing here calls a model.
		t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")
		_, lines, out := launch(t, `["codex"]`, `{}`)
		if !lines["PRESENT .codex"] {
			t.Errorf("the selected codex pack's ~/.codex is missing:\n%s", out)
		}
		for _, p := range []string{".claude", ".claude-shared-credentials", ".copilot", ".pi", ".gemini-shared-credentials"} {
			if !lines["ABSENT "+p] {
				t.Errorf("a codex-only jail has ~/%s, which no selected pack declares:\n%s", p, out)
			}
		}
		if !lines["LINK .claude.json"] || !lines["DANGLES .claude.json"] {
			t.Errorf("~/.claude.json should be a dangling link in a jail without claude (its "+
				"target, ~/.claude, is bound nowhere):\n%s", out)
		}
	})

	t.Run("writable_home_dirs", func(t *testing.T) {
		// The rootless done-condition's "a writable_home_dirs mount works": a rw bind of
		// <wsState>/writable-home/.skel-rw nested inside the :ro root, on a mountpoint the
		// host-side builder made. The STAT line beside it is the one to compare.
		_, lines, out := launch(t, `["claude"]`, `{"writable_home_dirs": [".skel-rw"]}`)
		if !lines["RWTOUCH ok"] {
			t.Errorf("writing into a writable_home_dirs entry under the :ro skeleton failed:\n%s", out)
		}
	})
}
