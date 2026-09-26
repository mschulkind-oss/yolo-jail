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
//     through the shared base) and its ~/.claude.json redirect dangles. Nor, with a machine
//     login PLANTED in the store first, is any .credentials.json or the planted oauthAccount
//     or refresh token readable anywhere under ~;
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
# Every path under ~, binds and links followed, that is a .credentials.json or holds a planted
# machine login. The markers are assembled here rather than spelled, so this script's own
# text can never be the match. The real tools, not a pack's blocked-tool shims.
export YOLO_BYPASS_SHIMS=1
seed="yolo-planted-seed"; seed="$seed@example.invalid"
token="yolo-planted-refresh"; token="$token-token"
find -L "$HOME" -name .credentials.json 2>/dev/null | while read -r f; do echo "CREDFILE $f"; done
find -L "$HOME" -type f -exec grep -l -F -e "$seed" -e "$token" {} + 2>/dev/null | while read -r f; do echo "LEAK $f"; done
echo "SEARCH done"
`

	// launch runs the probe in a fresh jail selecting packsJSON, with wsConfig as the
	// workspace config, and returns its lines, after checking the two properties every
	// podman jail must have.
	//
	// plant, when given, runs in the isolated HOME after it exists and before the launch.
	launch := func(t *testing.T, packsJSON, wsConfig string, plant ...func(t *testing.T)) (dir string, lines map[string]bool, out string) {
		t.Helper()
		dir = writeProject(t, wsConfig)
		packHome(t, `{"packs": `+packsJSON+`}`)
		for _, p := range plant {
			p(t)
		}
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
		// The host side, after the jail has exited: the skeleton it was bound from is GONE.
		// A launch removes the skeleton it built once the runtime answers that its container
		// no longer exists (OQ-BH16, docs/design/base-home-legacy-state.md). mountinfo names
		// the bind SOURCE, the skeleton's host path, so it can be checked directly.
		if _, err := os.Stat(root); err == nil {
			t.Errorf("the skeleton %s this jail was bound from still exists after its exit — "+
				"a launch removes its own skeleton once its container is known gone (OQ-BH16)", root)
		} else if !os.IsNotExist(err) {
			t.Errorf("stat %s: %v", root, err)
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
		// A machine login, in the machine store where a claude jail's login lands: the
		// Claude seed's oauthAccount and a shared refresh token. Planted rather than
		// assumed, so "not readable" is a negative about bytes that are really there, and
		// planted into a PRIVATE store (plantMachineLogin), never the developer's real one.
		_, lines, out := launch(t, `["codex"]`, `{}`, plantMachineLogin)
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
		if !lines["SEARCH done"] {
			t.Fatalf("the probe's search of ~ did not finish, so its silence proves nothing:\n%s", out)
		}
		for l := range lines {
			if strings.HasPrefix(l, "CREDFILE ") || strings.HasPrefix(l, "LEAK ") {
				t.Errorf("a jail that selected neither claude nor agy can read the machine's "+
					"Claude login: %s\n%s", l, out)
			}
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

// plantedMachineLogin is the machine login the codex-only subtest plants: the Claude seed's
// oauthAccount and a shared refresh token, keyed by their path under the machine store
// (paths.GlobalHome). The probe assembles the same two markers at run time.
var plantedMachineLogin = map[string]string{
	".claude/claude.json": `{"oauthAccount": {"emailAddress": "yolo-planted-seed@example.invalid"}, ` +
		`"hasCompletedOnboarding": true}`,
	".claude-shared-credentials/.credentials.json": `{"claudeAiOauth": {"refreshToken": "yolo-planted-refresh-token"}}`,
}

// plantMachineLogin writes plantedMachineLogin into a machine store PRIVATE to this test.
//
// ⚠ NOT THROUGH THE ISOLATED HOME'S STATE DIR AS IT STANDS. isolateHome links the whole of
// $HOME/.local/share/yolo-jail back to the machine's (packHomeSharedStores, so the image
// cache and flake bundle stay shared), so a write under $HOME/.local/share/yolo-jail/home
// follows that link and overwrites the developer's REAL Claude login: the shared-credentials
// dir every claude jail on the machine binds rw, and the seed every new workspace copies. A
// review caught the first version of this subtest doing exactly that. privateMachineStoreHome
// swaps the one `home` child for a per-test directory first, and the guard below refuses to
// write any path that still resolves outside the isolated HOME.
func plantMachineLogin(t *testing.T) {
	t.Helper()
	store := privateMachineStoreHome(t)
	isolated, err := filepath.EvalSymlinks(os.Getenv("HOME"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range plantedMachineLogin {
		p := filepath.Join(store, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			t.Fatal(err)
		}
		if r, err := filepath.Rel(isolated, dir); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			t.Fatalf("refusing to plant a fake login at %s: it resolves to %s, outside the "+
				"isolated HOME %s, so the write would land in a real store", p, dir, isolated)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// privateMachineStoreHome replaces the isolated HOME's $HOME/.local/share/yolo-jail link
// (seedPackHome's) with a real directory whose every child links to the machine's own,
// EXCEPT `home` — the machine store, paths.GlobalHome — which becomes a fresh directory of
// this test's. It returns that directory. Everything a launch shares by design (the image
// sentinel, the flake bundle, the embedded-pack lease, the agents and containers dirs) stays
// shared; only the machine login is this test's.
func privateMachineStoreHome(t *testing.T) string {
	t.Helper()
	state := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	fi, err := os.Lstat(state)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not seedPackHome's link to the machine's state dir (mode %v); "+
			"refusing to guess what it is", state, fi.Mode())
	}
	real, err := os.Readlink(state)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(real) {
		real = filepath.Join(filepath.Dir(state), real)
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "home" {
			continue
		}
		if err := os.Symlink(filepath.Join(real, e.Name()), filepath.Join(state, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(state, "home")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestPlantMachineLoginLeavesTheRealStoreAlone pins plantMachineLogin to a PRIVATE store.
// No container: it runs under -short, and it is the check that the codex-only subtest above
// cannot overwrite the machine's real Claude login through seedPackHome's state-dir link —
// which its first version did. A "machine" home holding a real login stands in for the
// developer's.
func TestPlantMachineLoginLeavesTheRealStoreAlone(t *testing.T) {
	realHome := resolvedTempDir(t)
	t.Setenv("HOME", realHome)
	realState := filepath.Join(realHome, ".local", "share", "yolo-jail")
	realCred := filepath.Join(realState, "home", ".claude-shared-credentials", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(realCred), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realCred, []byte("REAL-TOKEN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realState, "build"), 0o755); err != nil {
		t.Fatal(err)
	}

	packHome(t, `{"packs": ["codex"]}`)
	plantMachineLogin(t)

	if b, err := os.ReadFile(realCred); err != nil || string(b) != "REAL-TOKEN" {
		t.Errorf("the machine's real login was touched: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(realState, "home", ".claude", "claude.json")); !os.IsNotExist(err) {
		t.Errorf("a fake seed was written into the machine's real store: %v", err)
	}
	isoState := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail")
	for rel, body := range plantedMachineLogin {
		if b, err := os.ReadFile(filepath.Join(isoState, "home", filepath.FromSlash(rel))); err != nil || string(b) != body {
			t.Errorf("%s was not planted in the private store: %q, %v", rel, b, err)
		}
	}
	// Everything but `home` is still the machine's, so the launch keeps sharing the image
	// sentinel, the flake bundle and the rest.
	if got, err := os.Readlink(filepath.Join(isoState, "build")); err != nil || got != filepath.Join(realState, "build") {
		t.Errorf("the state dir's other children are no longer the machine's: build -> %q, %v", got, err)
	}
}
