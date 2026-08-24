package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// HOST_FILES SOURCE ENTRIES ON APPLE CONTAINER. A file entry emitted the same
// single-file bind that backend cannot do — and unlike the pack reads-host case, this
// one MASKS rather than omits: the entrypoint swallows the read error and writes the
// destination anyway at the readonly default mode, so the user gets an EMPTY 0o444 file
// where their .npmrc should be, unrepairable from inside the jail.
//
// A DIRECTORY entry is asserted unchanged in the same test, because that is the half
// that was already correct and the fix must not "helpfully" convert it — AC nests
// directory mounts fine.
func TestHostFileSourcesAreMaterializedOnAppleContainer(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, ".npmrc")
	if err := os.WriteFile(src, []byte("registry=https://example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirSrc := filepath.Join(home, "certs")
	if err := os.MkdirAll(dirSrc, 0o755); err != nil {
		t.Fatal(err)
	}

	in := hostFileIn(t,
		config.HostFileEntry{Path: ".npmrc", Source: src, Mode: config.HostFileModeReadonly},
		config.HostFileEntry{Path: "certs", Source: dirSrc, IsDir: true, Mode: config.HostFileModeReadonly},
	)
	in.rt = "container"
	if err := os.MkdirAll(in.wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	o := goldenOptions("/ws", home)
	args := strings.Join(o.hostUserFileArgs(in), " ")

	// The FILE entry: bytes present in the home, entrypoint pointed at them, no bind.
	fileEntry := config.HostFileEntry{Path: ".npmrc", Source: src, Mode: config.HostFileModeReadonly}
	got, err := os.ReadFile(filepath.Join(in.wsState, acCtxDirRel, "host-user", fileEntry.Slug()))
	if err != nil {
		t.Fatalf("the host_files FILE source was not materialized for Apple Container: %v\n"+
			"Left as a single-file bind it does not arrive, and prism writes the destination "+
			"anyway at 0o444 — an empty read-only file masking the user's real one.", err)
	}
	if string(got) != "registry=https://example\n" {
		t.Errorf("materialized content = %q, want the host file's bytes", got)
	}
	// The env var is emitted ONCE by assembleRunCmd for both host-file emitters (see
	// acCtxMaterialized); this emitter's job is to record that it materialized.
	if !in.acCtxMaterialized {
		t.Error("hostUserFileArgs materialized a grant without recording it, so assembleRunCmd " +
			"emits no YOLO_CTX_ROOT and the entrypoint still reads /ctx/host-user")
	}
	if strings.Contains(args, src+":/ctx/host-user/") {
		t.Errorf("a single-file /ctx bind survived on Apple Container:\n%s", args)
	}

	// The DIRECTORY entry must still be a plain bind — it was never broken.
	if !strings.Contains(args, dirSrc+":/ctx/host-user/") {
		t.Errorf("the directory entry lost its bind; AC nests directory mounts fine and this "+
			"half was already correct:\n%s", args)
	}
}

// podman must be untouched: it binds files into /ctx and must not gain the env var.
func TestHostFileSourcesStillBindOnPodman(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, ".npmrc")
	if err := os.WriteFile(src, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := hostFileIn(t, config.HostFileEntry{
		Path: ".npmrc", Source: src, Mode: config.HostFileModeReadonly,
	})
	in.rt = "podman"
	if err := os.MkdirAll(in.wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(goldenOptions("/ws", home).hostUserFileArgs(in), " ")
	if !strings.Contains(args, ":/ctx/host-user/") {
		t.Errorf("podman no longer binds the host_files source:\n%s", args)
	}
	if in.acCtxMaterialized {
		t.Error("podman recorded an Apple Container materialization; it mounts at /ctx and " +
			"must not trigger the remap")
	}
}

// ONE YOLO_CTX_ROOT, not two. Both host-file emitters can materialize in the same launch
// — a pack `reads-host` grant and a user `host_files` entry — and each used to append its
// own -e. A duplicated flag on a frozen argv is noise at best, and there is no evidence
// about how Apple Container treats a repeated -e.
func TestCtxRootEnvIsEmittedOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	// A pack reads-host grant (claude declares .claude/settings.json)...
	hostSettings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(hostSettings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostSettings, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// ...AND a user host_files entry, so both emitters fire.
	npmrc := filepath.Join(home, ".npmrc")
	if err := os.WriteFile(npmrc, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg: newConfig("security", sec), rt: "container", cname: "yolo-ws-abcd1234",
		packs:      claudePackFixture(t),
		hostFiles:  []config.HostFileEntry{{Path: ".npmrc", Source: npmrc, Mode: config.HostFileModeReadonly}},
		agentsPath: filepath.Join(ws, "agents"), wsState: wsState,
		miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})

	n := 0
	for _, a := range argv {
		if strings.HasPrefix(a, "YOLO_CTX_ROOT=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("YOLO_CTX_ROOT emitted %d times, want exactly 1 — both host-file emitters "+
			"materialized and each must not append its own:\n%s", n, strings.Join(argv, " "))
	}
}
