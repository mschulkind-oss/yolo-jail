package entrypoint

// hostfileredirect_test.go pins the macos-user half of a HOME-ROOT host_files destination
// (`~/.npmrc`): where it is laid, where its bytes land, and that the unconfined bootstrap writes
// it only through the home layout's own links.
//
// THE DEFECT IT CLOSES. On podman a home-root entry is a relative link in each jail's home
// skeleton, `~/.npmrc -> .config/yolo-home/<slug>`, and `~/.config` is the workspace's own
// sidecar, so every workspace keeps its own file. The macos-user layout redirected only core's
// three files (paths.HomeFileRedirects), so the same entry was rendered as a REAL file in the one
// account home every workspace shares: workspace B's launch overwrote A's `copy`, and a `once`
// seeded by A was never seeded for B at all.
//
// Every test here drives the real boot entry, RunDarwinBootstrap, against a real filesystem, so
// deleting the layout's host_files redirect, or the walk the host_files step makes through it,
// fails one of them. It runs on Linux for the reason darwinhomelayout_test.go gives: path
// resolution is the same on both kernels, and the backend cannot run in CI here.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// homeRootFixture is one account home shared by any number of workspaces, plus the relocated
// /ctx tree the launcher copies host bytes into (YOLO_CTX_ROOT, DP-L1).
type homeRootFixture struct {
	t        *testing.T
	base     string
	home     string
	packRoot string // "" boots with no pack
}

// newHomeRootFixture mints resolved paths (AGENTS.md's darwin rule: resolve where the path is
// minted) under a parent named like a macOS home with something under ~/Library, so every
// remedy below is also checked as one shell word.
func newHomeRootFixture(t *testing.T) *homeRootFixture {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(resolved, "Application Support")
	f := &homeRootFixture{t: t, base: base, home: filepath.Join(base, "home")}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_CTX_ROOT", filepath.Join(base, "ctx"))
	if err := os.MkdirAll(filepath.Dir(hostUserPath("probe")), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *homeRootFixture) ws(name string) string {
	return filepath.Join(f.base, "My Projects", name)
}

func (f *homeRootFixture) sidecar(name string) string {
	return filepath.Join(f.ws(name), ".yolo", "home")
}

// launch runs the real macos-user bootstrap for workspace `name` with entries as its
// host_files, after staging hostBytes as the launcher's copy of each source-bearing entry. It
// returns what the bootstrap printed and its error. A temp home can fail an unrelated
// generator (no git, no node), so callers assert on what they are about
// (requireHostFilesStepsOK, or the refusal's own words).
func (f *homeRootFixture) launch(name, hostBytes string, entries ...config.HostFileEntry) (string, error) {
	f.t.Helper()
	ws := f.ws(name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.SourceBearing() {
			if err := os.WriteFile(hostUserPath(entry.Slug()), []byte(hostBytes), 0o644); err != nil {
				f.t.Fatal(err)
			}
		}
	}
	vars := map[string]string{
		"HOME":                  f.home,
		"JAIL_HOME":             f.home,
		"YOLO_HOST_DIR":         ws,
		"YOLO_BLOCK_CONFIG":     `[]`,
		"YOLO_MISE_TOOLS":       `{}`,
		"YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv:    f.sidecar(name),
		"MISE_DATA_DIR":         filepath.Join(f.home, ".yolo", "mise"),
	}
	if f.packRoot != "" {
		vars["YOLO_PACK_ROOT"] = f.packRoot
	}
	e := DarwinEnvFrom(vars, f.home)
	if len(entries) > 0 {
		setHostFiles(f.t, e, entries...)
	}
	var out strings.Builder
	e.Stderr = &out
	err := RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	return out.String(), err
}

// requireHostFilesStepsOK fails when the layout or the host_files step failed; other generators
// may fail in a temp home, these two may not.
func requireHostFilesStepsOK(t *testing.T, said string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	for _, step := range []string{"darwin_home_layout", "configure_host_files"} {
		if strings.Contains(err.Error(), step) {
			t.Fatalf("the %s step failed: %v\nbootstrap said:\n%s", step, err, said)
		}
	}
}

func readOrAbsent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "<absent>"
	}
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TWO WORKSPACES, ONE ACCOUNT HOME, EACH ITS OWN ~/.npmrc — in every mode that renders a file.
// The account home holds the same RELATIVE link after both launches, the one podman's skeleton
// lays (config.HostFileEntry.SymlinkTarget), and each workspace's bytes stay in its own sidecar.
//
// Fails on the layout that redirected only core's three files: the entry was a real file in the
// shared home, so A's sidecar held nothing and B's launch decided what A read.
func TestHomeRootHostFilesArePerWorkspaceOnMacosUser(t *testing.T) {
	for _, mode := range []string{config.HostFileModeOnce, config.HostFileModeCopy, config.HostFileModeReadonly} {
		t.Run(mode, func(t *testing.T) {
			f := newHomeRootFixture(t)
			entry := config.HostFileEntry{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: mode}
			link := filepath.Join(f.home, ".npmrc")
			// ~/.config is the layout's link to <sidecar>/config, so that is where the link's
			// target lands for each workspace.
			inA := filepath.Join(f.sidecar("a"), "config", "yolo-home", entry.Slug())
			inB := filepath.Join(f.sidecar("b"), "config", "yolo-home", entry.Slug())

			said, err := f.launch("a", "registry=A\n", entry)
			requireHostFilesStepsOK(t, said, err)
			targetA, lerr := os.Readlink(link)
			if lerr != nil {
				t.Fatalf("~/.npmrc is not a symlink after workspace A's launch (%v): the entry was "+
					"rendered into the account home every workspace shares", lerr)
			}
			if targetA != entry.SymlinkTarget() {
				t.Errorf("~/.npmrc -> %q, want %q, the relative link podman's skeleton lays", targetA, entry.SymlinkTarget())
			}
			if got := readOrAbsent(t, inA); got != "registry=A\n" {
				t.Errorf("workspace A's sidecar holds %q at %s, want A's bytes", got, inA)
			}

			said, err = f.launch("b", "registry=B\n", entry)
			requireHostFilesStepsOK(t, said, err)
			targetB, _ := os.Readlink(link)
			if targetB != targetA {
				t.Errorf("workspace B's launch changed the link to %q; it is the same relative "+
					"string for every workspace and resolves through ~/.config", targetB)
			}
			if got := readOrAbsent(t, inB); got != "registry=B\n" {
				t.Errorf("workspace B's sidecar holds %q, want B's bytes", got)
			}
			if got := readOrAbsent(t, inA); got != "registry=A\n" {
				t.Errorf("workspace B's launch changed A's file to %q", got)
			}
			if got := readOrAbsent(t, link); got != "registry=B\n" {
				t.Errorf("~/.npmrc reads %q during B's session, want B's own file", got)
			}
			if mode == config.HostFileModeReadonly {
				if fi, err := os.Stat(inB); err != nil || fi.Mode().Perm() != 0o444 {
					t.Errorf("the readonly file in B's sidecar is %v (err %v), want 0444", fi.Mode().Perm(), err)
				}
			}

			// Back to A, whose host copy has moved on: `once` keeps A's first seed, the other two
			// re-render, and B's file is left as it was in every mode.
			said, err = f.launch("a", "registry=A2\n", entry)
			requireHostFilesStepsOK(t, said, err)
			want := "registry=A2\n"
			if mode == config.HostFileModeOnce {
				want = "registry=A\n"
			}
			if got := readOrAbsent(t, link); got != want {
				t.Errorf("~/.npmrc reads %q in A's second session, want %q", got, want)
			}
			if got := readOrAbsent(t, inB); got != "registry=B\n" {
				t.Errorf("A's second launch changed B's file to %q", got)
			}
		})
	}
}

// A LAUNCH THAT DOES NOT DECLARE THE ENTRY LEAVES THE LINK ALONE (P2: the layout manages what
// THIS launch declares). It then dangles, as the file is absent from a podman jail that does not
// declare it; removing it would take the file away from a running session of the workspace that
// does, and let a real file appear that the next declaring launch refuses.
func TestAnUndeclaringLaunchLeavesTheHomeRootLinkAlone(t *testing.T) {
	f := newHomeRootFixture(t)
	entry := config.HostFileEntry{Path: ".npmrc", Codec: "raw", Content: "registry=A\n", HasContent: true, Mode: config.HostFileModeOnce}
	said, err := f.launch("a", "", entry)
	requireHostFilesStepsOK(t, said, err)
	link := filepath.Join(f.home, ".npmrc")
	before, _ := os.Readlink(link)

	said, err = f.launch("b", "")
	requireHostFilesStepsOK(t, said, err)
	after, lerr := os.Readlink(link)
	if lerr != nil || after != before || before == "" {
		t.Fatalf("~/.npmrc -> %q (err %v) after a launch that does not declare it, want the "+
			"link left as %q", after, lerr, before)
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Errorf("~/.npmrc resolves in a workspace that never declared it (err %v)", err)
	}
	if got := readOrAbsent(t, filepath.Join(f.sidecar("a"), "config", "yolo-home", ".npmrc")); got != "registry=A\n" {
		t.Errorf("the undeclaring launch changed A's file to %q", got)
	}
}

// A REAL FILE WHERE THE LINK BELONGS IS REFUSED, NOT MIGRATED (OQ-HT2), and the remedy reaches
// that one file: it is what an earlier yolo rendered into the shared account home, so removing
// it costs nothing the new per-workspace file does not replace, while resetting the whole
// account would cost every workspace's machine tier. Nothing is written over it either — a
// write by the host_files step would land in the shared file the launch just refused.
func TestARealHomeRootFileWhereTheHostFileLinkBelongsRefuses(t *testing.T) {
	f := newHomeRootFixture(t)
	occupant := filepath.Join(f.home, ".npmrc")
	if err := os.WriteFile(occupant, []byte("rendered by an older launch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.HostFileEntry{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeCopy}

	said, err := f.launch("a", "registry=A\n", entry)
	if err == nil || !strings.Contains(err.Error(), "darwin_home_layout") {
		t.Fatalf("a real ~/.npmrc where the layout's link belongs did not refuse the layout step "+
			"(err %v)\n%s", err, said)
	}
	if !strings.Contains(said, occupant) {
		t.Errorf("the refusal does not name %s:\n%s", occupant, said)
	}
	if !offers(t, said, "sudo", "rm", occupant) {
		t.Errorf("the refusal does not offer `sudo rm %s` as a shell reads it; it offers %q:\n%s",
			occupant, offeredCommands(t, said, "sudo"), said)
	}
	if offers(t, said, "sudo", "rm", "-rf", f.home) {
		t.Errorf("the refusal prescribes resetting the whole account for one file a launch "+
			"rendered:\n%s", said)
	}
	if !strings.Contains(err.Error(), "configure_host_files") {
		t.Errorf("the host_files step wrote ~/.npmrc although the layout refused its link "+
			"(err %v)", err)
	}
	if got := readOrAbsent(t, occupant); got != "rendered by an older launch\n" {
		t.Errorf("the refused file was changed to %q", got)
	}
	if fi, err := os.Lstat(occupant); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the refused file was replaced (err %v)", err)
	}
}

// NOTHING IS WRITTEN THROUGH A LINK THE LAYOUT DID NOT LAY. The bootstrap runs outside Seatbelt
// as the sandbox account, which can write every workspace under the shared root, and the
// sidecar is in the agent-writable workspace. So a link the agent left below `~/.config` — at
// the staging directory, at the file itself, or below any other ~/.config destination — would
// carry this unconfined write into a directory the agent cannot reach, another workspace's
// .git here. The step refuses, names the link with the `sudo rm` that removes only it, and the
// other directory is byte-for-byte untouched.
//
// The third case is the one the old code already had for every ~/.config destination: it wrote
// through whatever the path resolved to (MEASURED on Linux against the real bootstrap,
// 2026-10-04: the file landed in the other workspace's .git).
func TestAHostFileIsNeverWrittenThroughALinkTheLayoutDidNotLay(t *testing.T) {
	npmrc := config.HostFileEntry{Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeCopy}
	nested := config.HostFileEntry{Path: ".config/yolo-it-tool/config", Source: "/host/config", Codec: "raw", Mode: config.HostFileModeCopy}
	cases := map[string]struct {
		entry config.HostFileEntry
		plant func(sidecar, other string) (link, target string)
	}{
		"a link at the staging directory": {npmrc, func(sidecar, other string) (string, string) {
			return filepath.Join(sidecar, "config", "yolo-home"), filepath.Join(other, ".git")
		}},
		"a link at the staged file": {npmrc, func(sidecar, other string) (string, string) {
			return filepath.Join(sidecar, "config", "yolo-home", ".npmrc"), filepath.Join(other, ".git", "config")
		}},
		"a link below ~/.config on the way to another destination": {nested, func(sidecar, other string) (string, string) {
			return filepath.Join(sidecar, "config", "yolo-it-tool"), filepath.Join(other, ".git")
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newHomeRootFixture(t)
			other := f.ws("other")
			writeTreeFile(t, filepath.Join(other, ".git", "config"), "[core]\n\tbare = false\n")

			said, err := f.launch("a", "first\n", tc.entry)
			requireHostFilesStepsOK(t, said, err)

			link, target := tc.plant(f.sidecar("a"), other)
			swapForLink(t, link, target)
			before := snapshotTree(t, other)

			said, err = f.launch("a", "second\n", tc.entry)
			if err == nil || !strings.Contains(err.Error(), "configure_host_files") {
				t.Errorf("the host_files step did not refuse a link the layout did not lay at %s "+
					"(err %v)", link, err)
			}
			if after := snapshotTree(t, other); !reflect.DeepEqual(before, after) {
				t.Fatalf("the bootstrap wrote into another workspace through the link at %s:\n"+
					"before %v\nafter  %v", link, before, after)
			}
			if !strings.Contains(said, link) {
				t.Errorf("the refusal does not name the link %s:\n%s", link, said)
			}
			if !offers(t, said, "sudo", "rm", link) {
				t.Errorf("the refusal does not offer `sudo rm %s` as a shell reads it; it offers %q:\n%s",
					link, offeredCommands(t, said, "sudo"), said)
			}
		})
	}
}

// A DESTINATION UNDER A PACK'S OWN DIRECTORY IS WRITTEN THROUGH THAT PACK'S LINK. The walk has
// to know every link THIS launch laid, the pack-declared ones included, or it would refuse
// `~/.claude` as a link it did not lay and fail every host_files entry under a selected pack's
// state dir.
func TestAHostFileUnderAPacksDirIsWrittenThroughThePacksLink(t *testing.T) {
	f := newHomeRootFixture(t)
	f.packRoot = stagePackForBootstrap(t, "claude")
	entry := config.HostFileEntry{Path: ".claude/yolo-it-extra.txt", Codec: "raw", Content: "extra\n", HasContent: true, Mode: config.HostFileModeCopy}
	said, err := f.launch("a", "", entry)
	requireHostFilesStepsOK(t, said, err)
	if got := readOrAbsent(t, filepath.Join(f.sidecar("a"), "claude", "yolo-it-extra.txt")); got != "extra\n" {
		t.Errorf("the entry under ~/.claude landed as %q in the workspace's claude dir, want its "+
			"bytes", got)
	}
}

// A DIRECTORY where a home-root host_files link belongs is offered `sudo rm -rf` of that one
// directory: `sudo rm` would fail on it, and a remedy that fails is no remedy.
func TestADirectoryWhereTheHostFileLinkBelongsIsOfferedItsOwnRemoval(t *testing.T) {
	f := newHomeRootFixture(t)
	occupant := filepath.Join(f.home, ".npmrc")
	writeTreeFile(t, filepath.Join(occupant, "kept"), "a directory somebody made\n")
	entry := config.HostFileEntry{Path: ".npmrc", Codec: "raw", Content: "x\n", HasContent: true, Mode: config.HostFileModeCopy}

	said, err := f.launch("a", "", entry)
	if err == nil || !strings.Contains(err.Error(), "darwin_home_layout") {
		t.Fatalf("a directory where the layout's link belongs did not refuse (err %v)\n%s", err, said)
	}
	if !offers(t, said, "sudo", "rm", "-rf", occupant) {
		t.Errorf("the refusal does not offer `sudo rm -rf %s`; it offers %q:\n%s",
			occupant, offeredCommands(t, said, "sudo"), said)
	}
	if offers(t, said, "sudo", "rm", "-rf", f.home) {
		t.Errorf("the refusal prescribes resetting the whole account:\n%s", said)
	}
	if got := readOrAbsent(t, filepath.Join(occupant, "kept")); got != "a directory somebody made\n" {
		t.Errorf("the refused directory's contents changed to %q", got)
	}
}
