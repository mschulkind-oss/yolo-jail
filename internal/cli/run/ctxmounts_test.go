package run

// The `mounts` config key, pinned at the CALL SITE (assembleRunCmd), not at
// splitMountSpec. Three agent-visible facts live here, and every one of them was
// a wrong belief a real agent held while writing a jail config (2026-08-21):
//
//   - a BARE host path lands at /ctx/<basename>, and the basename comes from the
//     SYMLINK-RESOLVED path — so ~/code/sysadmin behind a symlink does NOT land
//     at /ctx/sysadmin;
//   - :ro is not an option, it is the ONLY mode — there is no writable form of a
//     `mounts` entry;
//   - a docker-style ":ro" SUFFIX is not parsed as a mode. It is swallowed into
//     the host path, which then does not exist, and the mount is SKIPPED with a
//     warning. `yolo check` only warns too, so the config looks accepted.
//
// A pure splitMountSpec test would pass with the whole mounts block deleted from
// assembleRunCmd; these go through the assembler so they cannot.

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ctxMountArgs returns the -v pairs whose destination is under /ctx/, in argv
// order. It matches on the -v FLAG so no env value can be mistaken for a mount.
func ctxMountArgs(argv []string) []string {
	var out []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" && strings.Contains(argv[i+1], ":/ctx/") {
			out = append(out, argv[i+1])
		}
	}
	return out
}

// assembleWithMounts runs the assembler for rt with the given `mounts` list,
// returning the argv and whatever the assembler printed.
func assembleWithMounts(t *testing.T, rt string, mounts []any) ([]string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	var buf bytes.Buffer
	o.Stdout = &buf

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec, "mounts", mounts),
		rt:           rt,
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	return argv, buf.String()
}

// resolved mirrors resolvePath for the expected values: t.TempDir() sits under a
// symlink on macOS (/var → /private/var), so a hardcoded expectation would pass
// on Linux only.
func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return r
}

// TestCtxMountBarePathGetsCtxBasenameAndRO is the documented shape: a bare host
// path mounts read-only at /ctx/<basename>.
func TestCtxMountBarePathGetsCtxBasenameAndRO(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sysadmin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	argv, _ := assembleWithMounts(t, "podman", []any{dir})

	want := resolved(t, dir) + ":/ctx/sysadmin:ro"
	got := ctxMountArgs(argv)
	if len(got) != 1 || got[0] != want {
		t.Errorf("ctx mounts = %v, want [%q]", got, want)
	}
}

// TestCtxMountExplicitDestIsStillReadOnly: the "host:/dest" form picks the
// destination and NOTHING else. There is no writable form of a mounts entry, so
// :ro is appended either way.
func TestCtxMountExplicitDestIsStillReadOnly(t *testing.T) {
	dir := t.TempDir()

	argv, _ := assembleWithMounts(t, "podman", []any{dir + ":/ctx/lib"})

	want := resolved(t, dir) + ":/ctx/lib:ro"
	got := ctxMountArgs(argv)
	if len(got) != 1 || got[0] != want {
		t.Errorf("ctx mounts = %v, want [%q]", got, want)
	}
}

// TestCtxMountBasenameComesFromResolvedPath is the trap that sent an agent
// looking for /ctx/sysadmin: the /ctx name is the basename of the path AFTER
// symlink resolution, so a link whose name differs from its target's name mounts
// under the TARGET's name.
func TestCtxMountBasenameComesFromResolvedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sysadmin-config")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sysadmin")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}

	argv, _ := assembleWithMounts(t, "podman", []any{link})

	want := resolved(t, target) + ":/ctx/sysadmin-config:ro"
	got := ctxMountArgs(argv)
	if len(got) != 1 || got[0] != want {
		t.Errorf("ctx mounts = %v, want [%q] (the link's own name must NOT win)", got, want)
	}
}

// TestCtxMountDockerStyleROSuffixIsSkipped: ":ro" written as a third field is
// not a mode. splitMountSpec only splits on a colon followed by "/", so the
// whole string becomes the host path, that path does not exist, and the entry is
// dropped with a warning — a silently absent mount, not an error.
func TestCtxMountDockerStyleROSuffixIsSkipped(t *testing.T) {
	dir := t.TempDir()

	argv, printed := assembleWithMounts(t, "podman", []any{dir + ":/ctx/sysadmin:ro"})

	if got := ctxMountArgs(argv); got != nil {
		t.Errorf("a ':ro'-suffixed entry produced mounts %v, want none", got)
	}
	if !strings.Contains(printed, "mount path does not exist") {
		t.Errorf("expected a skip warning naming the bad path; printed: %q", printed)
	}
}

// TestWorkspaceIsReadWriteWhileCtxMountsAreNot pins the distinction the
// configuring-the-jail skill now leans on: the WORKSPACE (the cwd you launch
// from) is bound read-WRITE at /workspace, and a `mounts` entry is a separate,
// read-ONLY thing under /ctx. An agent that conflates the two reaches for
// `mounts` to get at the very directory it is already standing in.
func TestWorkspaceIsReadWriteWhileCtxMountsAreNot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sysadmin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	argv, _ := assembleWithMounts(t, "podman", []any{dir})

	// The workspace bind carries no mode suffix at all.
	if !slices.Contains(argv, "/ws:/workspace") {
		t.Errorf("workspace must be bound read-write at /workspace; argv: %v", argv)
	}
	if slices.Contains(argv, "/ws:/workspace:ro") {
		t.Error("the workspace bind must not be read-only")
	}
	// The mounts entry is somewhere else entirely, and it IS read-only.
	if got := ctxMountArgs(argv); len(got) != 1 || !strings.HasSuffix(got[0], ":ro") {
		t.Errorf("ctx mounts = %v, want exactly one :ro entry", got)
	}
}

// TestCtxMountSkippedWholesaleOnAppleContainer: Apple Container ignores :ro, so
// rather than hand out a writable context mount the assembler drops the mount
// and says why. Same config, different runtime, no /ctx at all.
func TestCtxMountSkippedWholesaleOnAppleContainer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sysadmin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	argv, printed := assembleWithMounts(t, "container", []any{dir})

	if got := ctxMountArgs(argv); got != nil {
		t.Errorf("Apple Container emitted ctx mounts %v, want none", got)
	}
	if !strings.Contains(printed, "read-only") {
		t.Errorf("expected the :ro-is-ignored explanation; printed: %q", printed)
	}
}
