package run

// acseed_test.go pins prepareWsState's per-backend layout, the fix for the Apple Container
// seed defect (docs/design/base-home-legacy-state.md#3-the-apple-container-seed-defect,
// OQ-BH12).
//
// prepareWsState stripped the leading dot of every path with no runtime branch, so on
// rt=container it synced the Claude login seed with wsState/claude/claude.json. But Apple
// Container binds wsState WHOLE at /home/agent, so that backend's ~/.claude.json is
// wsState/.claude.json: the seed never reached an Apple Container jail and never learned from
// one, and its homes carried stray undotted dirs (~/claude, ~/npm-global) nothing read.
//
// ⚠ UNMEASURED ON HARDWARE. These tests pin the host-side paths only. Whether a real Apple
// Container jail boots logged in from the seed needs a Mac; the Apple Container parity CI
// workflow is the instrument.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// writeSeed puts a logged-in Claude login seed in the machine store.
func writeSeed(t *testing.T) string {
	t.Helper()
	seed := filepath.Join(paths.GlobalHome(), ".claude", "claude.json")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"oauthAccount": {"emailAddress": "seed@example.invalid"}, "hasCompletedOnboarding": true}`
	if err := os.WriteFile(seed, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return seed
}

// readKeys decodes a JSON object file, failing the test if it is missing or malformed.
func readKeys(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decoding %s: %v", p, err)
	}
	return m
}

// A new Apple Container workspace starts logged in: the seed is forwarded into the file that
// backend's jail reads as ~/.claude.json, and NOT into podman's dot-stripped path.
func TestAppleContainerSeedReachesTheJailsClaudeJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeSeed(t)
	o := &Options{Workspace: t.TempDir()}
	wsState := o.prepareWsState(nil, packsFixture(t, "claude"), "container")

	got := readKeys(t, filepath.Join(wsState, ".claude.json"))
	if _, ok := got["oauthAccount"]; !ok {
		t.Errorf("wsState/.claude.json (the Apple Container jail's ~/.claude.json) got no "+
			"oauthAccount from the seed: %v", got)
	}
	if _, err := os.Lstat(filepath.Join(wsState, "claude", "claude.json")); err == nil {
		t.Error("the seed was also written to wsState/claude/claude.json, podman's path, which an " +
			"Apple Container jail sees as ~/claude/claude.json and never reads")
	}
}

// ...and the seed learns an Apple Container jail's login, from the same file.
func TestAppleContainerLoginIsLearnedByTheSeed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	o := &Options{Workspace: t.TempDir()}
	wsState := paths.WorkspaceHomeState(o.Workspace)
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsState, ".claude.json"),
		[]byte(`{"oauthAccount": {"emailAddress": "jail@example.invalid"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = o.prepareWsState(nil, packsFixture(t, "claude"), "container")

	seed := readKeys(t, filepath.Join(paths.GlobalHome(), ".claude", "claude.json"))
	if _, ok := seed["oauthAccount"]; !ok {
		t.Errorf("a logged-in Apple Container workspace's login did not back-propagate into "+
			"the seed: %v", seed)
	}
}

// The dirs follow each backend's layout: dotted, at the home path, on Apple Container, with
// none of podman's dot-stripped bind sources; and podman's layout unchanged.
func TestPrepareWsStateLaysOutEachBackendsOwnPaths(t *testing.T) {
	cfg := newConfig("writable_home_dirs", []any{".pi-lens"})
	podmanOnly := []string{"claude", "copilot", "npm-global", "local", "yolo-bin", "config",
		filepath.Join("pi", "agent"), "ssh", "bash_history", "yolo-bootstrap.sh",
		filepath.Join("writable-home", ".pi-lens")}

	t.Run("container", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeSeed(t)
		o := &Options{Workspace: t.TempDir()}
		wsState := o.prepareWsState(cfg, packsFixture(t, "claude", "copilot"), "container")
		for _, rel := range []string{".claude", ".copilot", "go"} {
			if fi, err := os.Stat(filepath.Join(wsState, rel)); err != nil || !fi.IsDir() {
				t.Errorf("wsState/%s missing (%v): Apple Container's jail reads it at ~/%s", rel, err, rel)
			}
		}
		for _, rel := range podmanOnly {
			if _, err := os.Lstat(filepath.Join(wsState, rel)); err == nil {
				t.Errorf("wsState/%s exists on Apple Container: it is a podman bind source, and "+
					"there it is a stray ~/%s in the jail's home", rel, rel)
			}
		}
	})

	t.Run("podman", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		writeSeed(t)
		o := &Options{Workspace: t.TempDir()}
		wsState := o.prepareWsState(cfg, packsFixture(t, "claude", "copilot"), "podman")
		for _, rel := range podmanOnly {
			if _, err := os.Lstat(filepath.Join(wsState, rel)); err != nil {
				t.Errorf("wsState/%s missing on podman (%v): it is a bind source, and a missing "+
					"one kills the container", rel, err)
			}
		}
		if got := readKeys(t, filepath.Join(wsState, "claude", "claude.json")); got["oauthAccount"] == nil {
			t.Errorf("podman's seed target wsState/claude/claude.json got no oauthAccount: %v", got)
		}
		for _, rel := range []string{".claude", ".copilot", ".claude.json"} {
			if _, err := os.Lstat(filepath.Join(wsState, rel)); err == nil {
				t.Errorf("wsState/%s exists on podman, which binds only the dot-stripped names", rel)
			}
		}
	})
}

// THE SEED NEVER WRITES THROUGH A LINK THE JAIL PLANTED, on either backend. The file
// claudeJSONInWsState names is one the jail can replace (a whole-home bind on Apple
// Container, the ~/.claude bind on podman), and prepareWsState syncs it on the host, as the
// host user, on the next fresh launch. So a link there used to aim a host write: the forward
// pass rewrote the link's target with the seed's JSON.
func TestTheSeedDoesNotWriteThroughAJailPlantedLink(t *testing.T) {
	for _, rt := range []string{"container", "podman"} {
		t.Run(rt, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeSeed(t)
			o := &Options{Workspace: t.TempDir()}
			wsState := paths.WorkspaceHomeState(o.Workspace)
			hostFile := filepath.Join(t.TempDir(), "authorized_keys")
			const victim = "ssh-ed25519 AAAA host-user-key\n"
			if err := os.WriteFile(hostFile, []byte(victim), 0o600); err != nil {
				t.Fatal(err)
			}
			link := claudeJSONInWsState(wsState, rt)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(hostFile, link); err != nil {
				t.Fatal(err)
			}

			_ = o.prepareWsState(nil, packsFixture(t, "claude"), rt)

			if got, _ := os.ReadFile(hostFile); string(got) != victim {
				t.Errorf("the seed sync wrote through the jail's link %s into the host file %s: %q",
					link, hostFile, got)
			}
		})
	}
}

// The one-time layout migrations land where THIS backend's jail reads the path. On Apple
// Container that is the dotted home path: podman's dot-stripped target (wsState/claude/…) is
// a stray ~/claude there, which OQ-BH12 stopped creating and nothing in the jail reads.
func TestLegacyMigrationsLandInEachBackendsLayout(t *testing.T) {
	for _, tc := range []struct {
		rt, claude, copilot, stray string
	}{
		{"container", ".claude", ".copilot", "claude"},
		{"podman", "claude", "copilot", ".claude"},
	} {
		t.Run(tc.rt, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			o := &Options{Workspace: t.TempDir()}
			wsState := paths.WorkspaceHomeState(o.Workspace)
			for rel, body := range map[string]string{
				filepath.Join("claude-projects", "s.jsonl"): "{}\n",
				"claude-settings.json":                      "{}\n",
				filepath.Join("copilot-sessions", "c.json"): "{}\n",
			} {
				p := filepath.Join(wsState, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			_ = o.prepareWsState(nil, packsFixture(t, "claude", "copilot"), tc.rt)

			for _, rel := range []string{
				filepath.Join(tc.claude, "projects", "s.jsonl"),
				filepath.Join(tc.claude, "settings.json"),
				filepath.Join(tc.copilot, "session-state", "c.json"),
			} {
				if _, err := os.Stat(filepath.Join(wsState, rel)); err != nil {
					t.Errorf("wsState/%s missing after the migration (%v): it is where a %s jail "+
						"reads it", rel, err, tc.rt)
				}
			}
			if _, err := os.Lstat(filepath.Join(wsState, tc.stray)); err == nil {
				t.Errorf("wsState/%s exists on %s: the migration wrote the other backend's layout",
					tc.stray, tc.rt)
			}
		})
	}
}

// THE SELECTION REACHES THE SKELETON AND THE BIND SOURCES. buildHomeSkeleton and
// preparePodmanBindSources each derive writable_home_dirs themselves, and each must derive
// it against the selected packs: a nil selection there accepts `.codex/sub` although codex is
// selected, giving the skeleton and wsState/writable-home a nested dir inside codex's own
// bind. Validation refuses such a config before either runs, so this drives both with an
// unvalidated one on purpose: it is the only way their own argument is observable.
func TestTheSkeletonAndTheBindSourcesDeriveWritableHomeDirsFromTheSelection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	packs := packsFixture(t, "claude", "codex")
	cfg := newConfig("writable_home_dirs", []any{".codex/sub", ".foo"})

	skeleton := buildSkeletonForTest(t, "yolo-whd-selection", packs, cfg, nil)
	o := &Options{Workspace: t.TempDir()}
	wsState := o.prepareWsState(cfg, packs, "podman")

	for _, root := range []struct{ name, dir string }{
		{"the skeleton", skeleton},
		{"wsState/writable-home", filepath.Join(wsState, "writable-home")},
	} {
		if _, err := os.Stat(filepath.Join(root.dir, ".foo")); err != nil {
			t.Errorf("%s has no .foo (%v); the fixture's legal entry must still be created", root.name, err)
		}
		if _, err := os.Lstat(filepath.Join(root.dir, ".codex", "sub")); err == nil {
			t.Errorf("%s has .codex/sub: writable_home_dirs was derived without the selection, "+
				"which reserves codex's .codex", root.name)
		}
	}
}
