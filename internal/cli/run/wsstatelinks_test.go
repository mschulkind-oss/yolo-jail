package run

// wsstatelinks_test.go pins that the host launcher never follows a link the jail planted in
// its own writable state. The workspace overlay, `<workspace>/.yolo/home`, is writable from
// inside every jail: Apple Container binds it whole at /home/agent, and on podman it is also
// reachable at /workspace/.yolo/home through the workspace bind (as is `.yolo` itself). The
// host's yolo reads, writes and copies there on the next launch, as the host user, so a link
// left at a path it touches, or at a directory above one, would aim that operation at a host
// path of the jail's choosing. And podman resolves a bind source on the host, so a link left
// at one binds whatever it points to into the next jail.
//
// Every case below plants the link, runs the production call site, and checks the host side.

import (
	"bytes"
	"go/ast"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostSecret is the content of a host file the jail wants copied somewhere it can read.
const hostSecret = "host-only-secret\n"

// secretTree is a host directory holding one secret file, standing in for ~/.ssh or ~/.aws.
func secretTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret.jsonl"), []byte(hostSecret), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// secretFile is a host file holding hostSecret.
func secretFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(secretTree(t), "secret.jsonl")
}

// writeFixture writes body at p, creating its parents.
func writeFixture(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertEmptyDir fails when the host directory dir gained any entry.
func assertEmptyDir(t *testing.T, dir, what string) {
	t.Helper()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("%s: the launch wrote %v into a host directory through the jail's link", what, names)
	}
}

// assertNoSecretBeneath fails when any REGULAR file under root (the walk does not follow
// links) holds hostSecret: the host copied a host file into the jail's state.
func assertNoSecretBeneath(t *testing.T, root string) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if b, _ := os.ReadFile(p); string(b) == hostSecret {
			t.Errorf("%s holds the host's secret: the launch copied a host file into the jail's "+
				"state through a link the jail planted", p)
		}
		return nil
	})
}

// assertRealDir fails unless every component of rel below root is a real directory.
func assertRealDir(t *testing.T, root, rel string) {
	t.Helper()
	for p := rel; p != "." && p != string(filepath.Separator); p = filepath.Dir(p) {
		fi, err := os.Lstat(filepath.Join(root, p))
		if err != nil || !fi.IsDir() {
			t.Errorf("%s is %v (err %v), want a real directory: podman resolves a linked bind "+
				"source on the host and binds whatever it points to", filepath.Join(root, p), modeOf(fi), err)
		}
	}
}

func modeOf(fi os.FileInfo) any {
	if fi == nil {
		return "missing"
	}
	return fi.Mode()
}

// prepareOn runs the production wsState preparation for a fresh workspace with the given
// packs, the way Run does, and returns wsState.
func prepareOn(t *testing.T, ws, rt string, cfg *jsonx.OrderedMap, packs []*packload.Pack) string {
	t.Helper()
	o := goldenOptions(ws, os.Getenv("HOME"))
	return o.prepareWsState(cfg, packs, rt)
}

// THE BIND SOURCES. preparePodmanBindSources creates every directory podman binds from the
// overlay, and podman follows a link at one: wsState/claude -> the host's ~/.claude would
// bind the host's real directory read-write into the next jail. A link at a bind source, or
// above one, must leave a real directory there and create nothing in the link's target.
func TestPodmanDirectoryBindSourcesAreNeverLinks(t *testing.T) {
	for _, tc := range []struct{ name, linked, bindSource string }{
		{"a link at a pack's state dir", "claude", "claude"},
		{"a link above a core overlay dir", "pi", "pi/agent"},
		{"a link at the ssh dir", "ssh", "ssh"},
		{"a link above a writable_home_dirs backing dir", config_writableHome, config_writableHome + "/.foo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ws := t.TempDir()
			wsState := paths.WorkspaceHomeState(ws)
			hostDir := t.TempDir()
			symlinkAt(t, hostDir, filepath.Join(wsState, tc.linked))

			prepareOn(t, ws, "podman", newConfig("writable_home_dirs", []any{".foo"}), claudePackFixture(t))

			assertEmptyDir(t, hostDir, tc.name)
			assertRealDir(t, wsState, filepath.FromSlash(tc.bindSource))
		})
	}
}

// config_writableHome is config.WritableHomeBackingSubdir, spelled out so the table reads.
const config_writableHome = "writable-home"

// Apple Container creates the selected packs' dirs at their dotted names; a pack dir with a
// second segment was created through a link above it. The link itself is left alone there:
// on this backend wsState IS the jail's home, a link in it is the jail user's own, and the
// guest resolves it, never a host bind.
func TestAppleContainerPackDirsAreNeverCreatedThroughALink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	wsState := paths.WorkspaceHomeState(ws)
	hostDir := t.TempDir()
	symlinkAt(t, hostDir, filepath.Join(wsState, ".two"))
	pack := workspaceFilesPack(t, "twoseg", "f.txt", ".two/seg/f.txt", ".two/seg")

	prepareOn(t, ws, "container", nil, []*packload.Pack{pack})

	assertEmptyDir(t, hostDir, "wsState/.two linked")
}

// THE SINGLE-FILE BIND SOURCES. touchFile created them with O_CREATE, which follows a
// dangling link and creates its target, and left a link to an existing file in place, which
// podman then binds: wsState/bash_history -> ~/.ssh/id_ed25519 would hand the jail the key.
func TestPodmanFileBindSourcesAreNeverLinks(t *testing.T) {
	t.Run("a dangling link", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		ws := t.TempDir()
		wsState := paths.WorkspaceHomeState(ws)
		hostDir := t.TempDir()
		symlinkAt(t, filepath.Join(hostDir, "created"), filepath.Join(wsState, "bash_history"))

		prepareOn(t, ws, "podman", nil, nil)

		assertEmptyDir(t, hostDir, "a dangling bash_history link")
		if fi, err := os.Lstat(filepath.Join(wsState, "bash_history")); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("wsState/bash_history is %v (err %v), want a regular file", modeOf(fi), err)
		}
	})
	t.Run("a link to a host file", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		ws := t.TempDir()
		wsState := paths.WorkspaceHomeState(ws)
		host := outsideFile(t)
		symlinkAt(t, host, filepath.Join(wsState, "yolo-perf.log"))

		prepareOn(t, ws, "podman", nil, nil)

		if got := mustReadFile(t, host); got != hostFileBody {
			t.Errorf("the host file behind the link changed: %q", got)
		}
		if fi, err := os.Lstat(filepath.Join(wsState, "yolo-perf.log")); err != nil || !fi.Mode().IsRegular() {
			t.Errorf("wsState/yolo-perf.log is %v (err %v), want a regular file replacing the link "+
				"podman would otherwise bind", modeOf(fi), err)
		}
	})
}

// THE LEGACY LAYOUT MIGRATIONS read FROM the overlay and write INTO it. Following a link on
// the read side copies a host tree (~/.ssh, say) into the jail's own state, where the jail
// reads it; following one on the write side creates files wherever it points.
func TestLegacyOverlayMigrationsNeverFollowALink(t *testing.T) {
	for _, rt := range []string{"podman", "container"} {
		t.Run(rt, func(t *testing.T) {
			projects := func(wsState string) string { return wsStateHomePath(wsState, rt, ".claude/projects") }
			settings := func(wsState string) string { return wsStateHomePath(wsState, rt, ".claude/settings.json") }
			sessions := func(wsState string) string { return wsStateHomePath(wsState, rt, ".copilot/session-state") }
			packs := func(t *testing.T) []*packload.Pack { return packsFixture(t, "claude", "copilot") }

			t.Run("a linked legacy directory", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ws := t.TempDir()
				wsState := paths.WorkspaceHomeState(ws)
				symlinkAt(t, secretTree(t), filepath.Join(wsState, "claude-projects"))
				symlinkAt(t, secretTree(t), filepath.Join(wsState, "copilot-sessions"))

				prepareOn(t, ws, rt, nil, packs(t))

				assertNoSecretBeneath(t, projects(wsState))
				assertNoSecretBeneath(t, sessions(wsState))
			})
			t.Run("links inside a legacy directory", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ws := t.TempDir()
				wsState := paths.WorkspaceHomeState(ws)
				legacy := filepath.Join(wsState, "claude-projects")
				writeFixture(t, filepath.Join(legacy, "real.jsonl"), "mine\n")
				symlinkAt(t, secretFile(t), filepath.Join(legacy, "leak.jsonl"))
				symlinkAt(t, secretTree(t), filepath.Join(legacy, "sub"))

				prepareOn(t, ws, rt, nil, packs(t))

				assertNoSecretBeneath(t, projects(wsState))
				if got := mustReadFile(t, filepath.Join(projects(wsState), "real.jsonl")); got != "mine\n" {
					t.Errorf("the migration no longer carries a regular legacy file: %q", got)
				}
			})
			t.Run("a linked target directory", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ws := t.TempDir()
				wsState := paths.WorkspaceHomeState(ws)
				writeFixture(t, filepath.Join(wsState, "claude-projects", "x.jsonl"), "mine\n")
				hostDir := t.TempDir()
				symlinkAt(t, hostDir, projects(wsState))

				prepareOn(t, ws, rt, nil, packs(t))

				assertEmptyDir(t, hostDir, "a linked projects target")
			})
			t.Run("a link at a target file", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ws := t.TempDir()
				wsState := paths.WorkspaceHomeState(ws)
				writeFixture(t, filepath.Join(wsState, "claude-projects", "x.jsonl"), "mine\n")
				writeFixture(t, filepath.Join(wsState, "claude-settings.json"), `{"theme":"dark"}`)
				hostDir := t.TempDir()
				symlinkAt(t, filepath.Join(hostDir, "x.jsonl"), filepath.Join(projects(wsState), "x.jsonl"))
				symlinkAt(t, filepath.Join(hostDir, "settings.json"), settings(wsState))

				prepareOn(t, ws, rt, nil, packs(t))

				assertEmptyDir(t, hostDir, "dangling links at the migration's targets")
			})
			t.Run("a linked legacy settings file", func(t *testing.T) {
				t.Setenv("HOME", t.TempDir())
				ws := t.TempDir()
				wsState := paths.WorkspaceHomeState(ws)
				symlinkAt(t, secretFile(t), filepath.Join(wsState, "claude-settings.json"))

				prepareOn(t, ws, rt, nil, packs(t))

				if b, err := os.ReadFile(settings(wsState)); err == nil && string(b) == hostSecret {
					t.Errorf("%s holds the host file the legacy settings link named", settings(wsState))
				}
			})
		})
	}
}

// THE SHARED-DIR RESCUE copies FROM this workspace's overlay INTO the machine store, whose
// shared dirs every jail with the pack mounts read-write. Following a link on the read side
// would publish a host tree to every such jail; on the write side, create files wherever the
// link in the (jail-writable) shared dir points.
func TestSharedDirRescueNeverFollowsALink(t *testing.T) {
	shared := packload.SharedDirs(claudePackFixture(t))[0]
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, wsState, global, hostDir string)
	}{
		{"a linked stranded dir", func(t *testing.T, wsState, global, hostDir string) {
			symlinkAt(t, secretTree(t), filepath.Join(wsState, shared))
		}},
		{"a link inside the stranded dir", func(t *testing.T, wsState, global, hostDir string) {
			symlinkAt(t, secretFile(t), filepath.Join(wsState, shared, "creds.json"))
			writeFixture(t, filepath.Join(wsState, shared, "other.json"), "{}")
		}},
		{"a link at the machine-wide target", func(t *testing.T, wsState, global, hostDir string) {
			sharedCredFixture(t, wsState, shared, `{"token":"stranded"}`)
			symlinkAt(t, filepath.Join(hostDir, "creds.json"), filepath.Join(global, "creds.json"))
		}},
		{"a linked dir in the machine-wide target", func(t *testing.T, wsState, global, hostDir string) {
			writeFixture(t, filepath.Join(wsState, shared, "sub", "f.json"), "{}")
			symlinkAt(t, hostDir, filepath.Join(global, "sub"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ws := t.TempDir()
			wsState := paths.WorkspaceHomeState(ws)
			global := filepath.Join(paths.GlobalHome(), shared)
			hostDir := t.TempDir()
			tc.plant(t, wsState, global, hostDir)

			prepareWithClaudePack(t, ws)

			assertNoSecretBeneath(t, global)
			assertEmptyDir(t, hostDir, tc.name)
		})
	}
}

// THE SEED SYNC through its call site: on podman the workspace side is
// wsState/claude/claude.json, and a jail that replaced wsState/claude with a link to a host
// directory had the forward pass create <that dir>/claude.json holding the seed's login.
func TestSeedSyncWritesNoLoginThroughALinkedClaudeDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeSeed(t)
	ws := t.TempDir()
	hostDir := t.TempDir()
	symlinkAt(t, hostDir, filepath.Join(paths.WorkspaceHomeState(ws), "claude"))

	prepareOn(t, ws, "podman", nil, packsFixture(t, "claude"))

	assertEmptyDir(t, hostDir, "wsState/claude linked")
}

// THE ROOT ITSELF. `.yolo` and `.yolo/home` are both reachable from the jail through the
// workspace bind, so either can be a link, and every write below it would follow. Nothing
// may be written into the link's target.
func TestPrepareWsStateWritesNothingThroughALinkedRoot(t *testing.T) {
	for _, linked := range []string{".yolo/home", ".yolo"} {
		t.Run(linked, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			writeSeed(t)
			ws := t.TempDir()
			hostDir := t.TempDir()
			if linked == ".yolo" {
				// A host directory already shaped like the state dir, so the link resolves.
				if err := os.MkdirAll(filepath.Join(hostDir, "home"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			symlinkAt(t, hostDir, filepath.Join(ws, filepath.FromSlash(linked)))
			before := treeNames(t, hostDir)

			prepareOn(t, ws, "podman", newConfig("writable_home_dirs", []any{".foo"}), packsFixture(t, "claude", "copilot"))

			if after := treeNames(t, hostDir); after != before {
				t.Errorf("prepareWsState wrote through the linked %s into a host directory:\nbefore %q\nafter  %q",
					linked, before, after)
			}
		})
	}
}

// treeNames lists every path under root, one per line, for a before/after comparison.
func treeNames(t *testing.T, root string) string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != root {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	return strings.Join(out, "\n")
}

// THE NESTED-BIND DEREFERENCE COPY. ROFileMountArg copies a host file that is itself a bind
// mountpoint to wsState/rel and mounts the copy; that copy followed a link at rel, or above
// it, onto a host file.
func TestROFileMountArgDerefNeverWritesThroughALink(t *testing.T) {
	host := filepath.Join(t.TempDir(), "cfg.json")
	writeFixture(t, host, "data")
	targets := map[string]struct{}{host: {}}

	t.Run("a link at the copy", func(t *testing.T) {
		wsState := t.TempDir()
		victim := outsideFile(t)
		symlinkAt(t, victim, filepath.Join(wsState, "sub", "cfg.json"))

		ROFileMountArg(host, "/home/agent/cfg.json", wsState, "sub/cfg.json", targets, nil)

		if got := mustReadFile(t, victim); got != hostFileBody {
			t.Errorf("the dereference copy wrote through the jail's link onto a host file: %q", got)
		}
		if got := mustReadFile(t, filepath.Join(wsState, "sub", "cfg.json")); got != "data" {
			t.Errorf("the copy did not land in the overlay: %q", got)
		}
	})
	t.Run("a link above the copy", func(t *testing.T) {
		wsState := t.TempDir()
		hostDir := t.TempDir()
		symlinkAt(t, hostDir, filepath.Join(wsState, "sub"))

		args := ROFileMountArg(host, "/home/agent/cfg.json", wsState, "sub/cfg.json", targets, nil)

		assertEmptyDir(t, hostDir, "wsState/sub linked")
		if want := host + ":/home/agent/cfg.json:ro"; len(args) != 2 || args[1] != want {
			t.Errorf("a refused copy must fall back to the direct mount %q, got %v", want, args)
		}
	})
}

// THE COMPOSED GITCONFIG is written straight into the overlay: wsState/yolo-gitconfig on
// podman, wsState/.config/git/config on Apple Container.
func TestGitIdentityNeverWritesThroughALink(t *testing.T) {
	probes := map[string]string{
		"git config --get user.name":  "Ada\n",
		"git config --get user.email": "ada@example.com\n",
	}
	for _, tc := range []struct{ name, rt, linked, file string }{
		{"podman, a link at the staged file", "podman", "yolo-gitconfig", "yolo-gitconfig"},
		{"container, a link at the config", "container", ".config/git/config", ".config/git/config"},
		{"container, a link above the config", "container", ".config/git", ".config/git/config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			o, wsState := gitIdentityTestOpts(t, probes)
			victim := outsideFile(t)
			hostDir := t.TempDir()
			target := victim
			if tc.linked != tc.file {
				target = hostDir
			}
			symlinkAt(t, target, filepath.Join(wsState, filepath.FromSlash(tc.linked)))

			o.gitIdentityMountArgs(tc.rt, wsState, map[string]struct{}{})

			if got := mustReadFile(t, victim); got != hostFileBody {
				t.Errorf("the composed gitconfig was written through the jail's link onto a host file: %q", got)
			}
			assertEmptyDir(t, hostDir, tc.name)
			if tc.linked == tc.file {
				if got := mustReadFile(t, filepath.Join(wsState, filepath.FromSlash(tc.file))); !strings.Contains(got, "ada@example.com") {
					t.Errorf("the composed gitconfig did not land in the overlay: %q", got)
				}
			}
		})
	}
}

// THE APPLE CONTAINER PACK TREE is REPLACED each launch (acMaterializeTree): a RemoveAll
// of wsState/.yolo-packs. Through a linked overlay that removed a host directory.
func TestACPackTreeCopyNeverRemovesThroughALink(t *testing.T) {
	src := t.TempDir()
	writeFixture(t, filepath.Join(src, "claude", "pack.json"), "{}")

	t.Run("a link at the tree", func(t *testing.T) {
		wsState := t.TempDir()
		hostDir := t.TempDir()
		writeFixture(t, filepath.Join(hostDir, "keep.txt"), "keep\n")
		symlinkAt(t, hostDir, filepath.Join(wsState, acPackRootRel))

		if err := acMaterializeTree(src, acPackRootRel, wsState); err != nil {
			t.Fatalf("acMaterializeTree: %v", err)
		}

		if got := mustReadFile(t, filepath.Join(hostDir, "keep.txt")); got != "keep\n" {
			t.Errorf("the host directory behind the link lost its file: %q", got)
		}
		if got := mustReadFile(t, filepath.Join(wsState, acPackRootRel, "claude", "pack.json")); got != "{}" {
			t.Errorf("the pack tree did not land in the overlay: %q", got)
		}
	})
	t.Run("a linked overlay", func(t *testing.T) {
		ws := t.TempDir()
		hostDir := t.TempDir()
		writeFixture(t, filepath.Join(hostDir, acPackRootRel, "keep.txt"), "keep\n")
		wsState := paths.WorkspaceHomeState(ws)
		symlinkAt(t, hostDir, wsState)

		_ = acMaterializeTree(src, acPackRootRel, wsState)

		if _, err := os.Stat(filepath.Join(hostDir, acPackRootRel, "keep.txt")); err != nil {
			t.Errorf("the tree copy removed a host directory through the linked overlay: %v", err)
		}
		if _, err := os.Stat(filepath.Join(hostDir, acPackRootRel, "claude")); err == nil {
			t.Error("the tree copy wrote the pack tree into a host directory through the linked overlay")
		}
	})
}

// THE USER-ENV FILE holds every hydrated env_sources value and is rewritten, and chmodded,
// on every launch and every attach.
func TestUserEnvFileNeverWritesThroughALink(t *testing.T) {
	wsState := t.TempDir()
	victim := outsideFile(t)
	symlinkAt(t, victim, filepath.Join(wsState, "yolo-user-env.sh"))
	env := jsonx.NewOrderedMap()
	env.Set("FOO", "bar")

	writeUserEnvFile(filepath.Join(wsState, "yolo-user-env.sh"), env, nil)

	if got := mustReadFile(t, victim); got != hostFileBody {
		t.Errorf("the env file was written through the jail's link onto a host file: %q", got)
	}
	if fi, _ := os.Stat(victim); fi == nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("the host file behind the link was chmodded: %v", modeOf(fi))
	}
	fi, err := os.Lstat(filepath.Join(wsState, "yolo-user-env.sh"))
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != userEnvFileMode {
		t.Fatalf("wsState/yolo-user-env.sh is %v (err %v), want a regular %04o file", modeOf(fi), err, userEnvFileMode)
	}
	if got := mustReadFile(t, filepath.Join(wsState, "yolo-user-env.sh")); !strings.Contains(got, "FOO") {
		t.Errorf("the env file lost its content: %q", got)
	}
}

// THE INHERITED USER SCOPE is staged in the overlay (podman) or written to its destination
// inside it (Apple Container).
func TestInheritedUserScopeNeverWritesThroughALink(t *testing.T) {
	for _, tc := range []struct{ name, rt, linked, victimKind string }{
		{"podman, a link at the staged file", "podman", "inherit-preflight.jsonc", "file"},
		{"container, a link at the destination", "container", inheritPreflightRel, "file"},
		{"container, a link above the destination", "container", ".config/yolo-jail", "dir"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, wsState := inheritHome(t, `{"packs": ["claude"]}`)
			o := inheritOptions(t)
			victim := outsideFile(t)
			hostDir := t.TempDir()
			target := victim
			if tc.victimKind == "dir" {
				target = hostDir
			}
			symlinkAt(t, target, filepath.Join(wsState, filepath.FromSlash(tc.linked)))

			o.userConfigMountArgs(tc.rt, wsState)

			if got := mustReadFile(t, victim); got != hostFileBody {
				t.Errorf("the inherited scope was written through the jail's link onto a host file: %q", got)
			}
			assertEmptyDir(t, hostDir, tc.name)
		})
	}
}

// THE PER-SIDE SHADOW BACKING DIRS are podman bind sources at /workspace/<rel>: a link at
// one, or at venv-shadows itself, bound its target read-write into the jail.
func TestVenvShadowBackingDirsAreNeverLinks(t *testing.T) {
	for _, linked := range []string{"venv-shadows", "venv-shadows/.venv"} {
		t.Run(linked, func(t *testing.T) {
			ws := t.TempDir()
			wsState := t.TempDir()
			hostDir := t.TempDir()
			symlinkAt(t, hostDir, filepath.Join(wsState, filepath.FromSlash(linked)))
			o := goldenOptions(ws, t.TempDir())

			o.venvShadowMountArgs(jsonx.NewOrderedMap(), wsState)

			assertEmptyDir(t, hostDir, linked)
			assertRealDir(t, wsState, filepath.Join("venv-shadows", ".venv"))
		})
	}
}

// THE LAUNCH REFUSES A LINKED `.yolo` OR `.yolo/home` before its first write there (the
// launch log's tee), naming the path. Past that point every host write below the link would
// follow it, and podman would bind whatever the bind sources under it resolve to.
func TestRunRefusesALinkedWorkspaceState(t *testing.T) {
	for _, linked := range []string{".yolo", ".yolo/home"} {
		t.Run(linked, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			ws := t.TempDir()
			hostDir := t.TempDir()
			if linked == ".yolo/home" {
				if err := os.MkdirAll(filepath.Join(ws, ".yolo"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(ws, filepath.FromSlash(linked))
			symlinkAt(t, hostDir, link)
			var stdout, stderr bytes.Buffer
			o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
			o.Getenv = guardEnv(nil)

			if rc := Run(*o); rc != 1 {
				t.Fatalf("a launch whose %s is a link must refuse, rc=%d:\n%s%s",
					linked, rc, stdout.String(), stderr.String())
			}
			out := stdout.String() + stderr.String()
			for _, want := range []string{"Refusing to launch", link, "symbolic link"} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal should name %q:\n%s", want, out)
				}
			}
			assertEmptyDir(t, hostDir, "a refused launch")
		})
	}
}

// The call-site pin: the refusal must stay above the launch log's first write under .yolo.
func TestRunCallsTheLinkedStateGuardBeforeTheLaunchLog(t *testing.T) {
	pos := firstCallsInRun(t, "linkedWorkspaceState", "attachLaunchLog", "stageRunPacks")
	if pos["linkedWorkspaceState"] == token.NoPos {
		t.Fatal("Run no longer calls linkedWorkspaceState — a launch whose .yolo or .yolo/home " +
			"is a link the jail left would write through it and bind through it. If the guard " +
			"moved, move this pin with it rather than deleting it.")
	}
	if logPos := pos["attachLaunchLog"]; logPos == token.NoPos || pos["linkedWorkspaceState"] > logPos {
		t.Fatal("the linked-state guard sits below attachLaunchLog (or Run lost the launch log): " +
			"the tee would write .yolo/launch.log through the link before the refusal fires.")
	}
	if stagePos := pos["stageRunPacks"]; stagePos != token.NoPos && pos["linkedWorkspaceState"] > stagePos {
		t.Fatal("the linked-state guard sits below pack staging.")
	}
}

// THE host_files BACKING DIRS: a writable_home_dirs-shaped backing dir is a podman bind
// source, and the yolo-home dir the entrypoint writes a home-root file into sits inside the
// .config overlay.
func TestHostFilesBackingDirsAreNeverLinks(t *testing.T) {
	t.Run("a link at the writable-home backing root", func(t *testing.T) {
		wsState := t.TempDir()
		hostDir := t.TempDir()
		symlinkAt(t, hostDir, filepath.Join(wsState, config_writableHome))
		entries := []config.HostFileEntry{{Path: "foo/a.json", Codec: "json", HasContent: true}}

		prepareHostFiles(wsState, entries, nil, nil)

		assertEmptyDir(t, hostDir, "wsState/writable-home linked")
		assertRealDir(t, wsState, filepath.Join(config_writableHome, "foo"))
	})
	t.Run("a link at the config overlay", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		wsState := t.TempDir()
		hostDir := t.TempDir()
		symlinkAt(t, hostDir, filepath.Join(wsState, "config"))
		entries := []config.HostFileEntry{{
			Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly,
		}}

		prepareHostFiles(wsState, entries, nil, nil)

		assertEmptyDir(t, hostDir, "wsState/config linked")
	})
}

// THE ROOT OPENER refuses a link at the root or at its parent, naming the path, because
// os.OpenRoot follows one and the containment is only as good as the directory it is opened
// on. A missing root is created, as the plain MkdirAll it replaced did.
func TestStateRootRefusesALinkNamingIt(t *testing.T) {
	for _, tc := range []struct {
		name     string
		atParent bool
	}{{"a link at the root", false}, {"a link at its parent", true}} {
		t.Run(tc.name, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), ".yolo")
			hostDir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(hostDir, "home"), 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(parent, "home")
			if tc.atParent {
				link = parent
			}
			symlinkAt(t, hostDir, link)

			r, err := openStateRoot(filepath.Join(parent, "home"))
			if err == nil {
				r.Close()
				t.Fatalf("openStateRoot opened a root through the link at %s", link)
			}
			if !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
				t.Errorf("the refusal does not name the link %s: %v", link, err)
			}
		})
	}
	t.Run("a missing root is created", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".yolo", "home")
		r, err := openStateRoot(root)
		if err != nil {
			t.Fatalf("openStateRoot(%s): %v", root, err)
		}
		r.Close()
		assertRealDir(t, filepath.Dir(filepath.Dir(root)), filepath.Join(".yolo", "home"))
	})
}

// THE HANDOFF POINTER is read on the host and its content becomes a section of the jail's
// briefing, which the jail reads. `.yolo` is jail-writable, so a link planted at
// .yolo/handover.md would copy any host file the link names (a private key, say) into the
// jail. A pointer that is not a regular file is not read.
func TestReadHandoffNeverReadsThroughALink(t *testing.T) {
	t.Run("a link at the pointer", func(t *testing.T) {
		ws := t.TempDir()
		symlinkAt(t, secretFile(t), filepath.Join(ws, ".yolo", handoffPointer))

		if got := readHandoff(ws); strings.Contains(got, hostSecret) {
			t.Errorf("readHandoff read the host file behind the jail's link into the briefing: %q", got)
		}
	})
	t.Run("a regular pointer is still read", func(t *testing.T) {
		ws := t.TempDir()
		writeFixture(t, filepath.Join(ws, ".yolo", handoffPointer), "do the thing\n")

		if got := readHandoff(ws); got != "do the thing\n" {
			t.Errorf("readHandoff = %q, want the filed handoff", got)
		}
	})
}

// A REPLACED host_files BACKING-DIR LINK IS NAMED, as every other bind source's is: the jail may
// have left the link, and what the user expected behind it is gone. prepareHostFiles returns
// each replacement and runContainer prints them (printReplacedLinks); before this the list was
// discarded and the replacement was silent.
func TestHostFilesNamesEachReplacedBackingLink(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dangling bool
	}{{"a link to a host dir", false}, {"a dangling link", true}} {
		t.Run(tc.name, func(t *testing.T) {
			wsState := t.TempDir()
			hostDir := filepath.Join(t.TempDir(), "victim")
			if !tc.dangling {
				if err := os.MkdirAll(hostDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			linkRel := filepath.Join(config_writableHome, "foo")
			if err := os.MkdirAll(filepath.Join(wsState, config_writableHome), 0o755); err != nil {
				t.Fatal(err)
			}
			symlinkAt(t, hostDir, filepath.Join(wsState, linkRel))
			entries := []config.HostFileEntry{{Path: "foo/a.json", Codec: "json", HasContent: true}}

			replaced := prepareHostFiles(wsState, entries, nil, nil)

			var buf bytes.Buffer
			printReplacedLinks((&Options{}).pr(&buf), wsState, replaced)
			want := "Replaced a symbolic link at " + filepath.Join(wsState, linkRel)
			if !strings.Contains(buf.String(), want) {
				t.Errorf("the replaced backing-dir link was not named; got %q, want a line containing %q",
					buf.String(), want)
			}
			assertRealDir(t, wsState, linkRel)
			if !tc.dangling {
				assertEmptyDir(t, hostDir, "the backing dir's link target")
			} else if _, err := os.Lstat(hostDir); err == nil {
				t.Errorf("prepareHostFiles created the dangling link's target %s", hostDir)
			}
		})
	}
}

// THE CALL SITE of the notice above: runContainer hands prepareHostFiles' result straight to
// printReplacedLinks. The behavioral test pins the callee; this fails if the print is deleted.
func TestRunContainerPrintsHostFilesReplacedLinks(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	found := false
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || skelCallee(call) != "printReplacedLinks" || len(call.Args) != 3 {
			return true
		}
		if inner, ok := call.Args[2].(*ast.CallExpr); ok && skelCallee(inner) == "prepareHostFiles" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("runContainer no longer passes prepareHostFiles' replaced links to printReplacedLinks, " +
			"so a link replaced at a host_files backing dir goes unnamed")
	}
}
