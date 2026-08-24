package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
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
	if !strings.Contains(args, "YOLO_CTX_ROOT=/home/agent/"+acCtxDirRel) {
		t.Errorf("no YOLO_CTX_ROOT emitted, so the entrypoint still reads /ctx/host-user:\n%s", args)
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
	if strings.Contains(args, "YOLO_CTX_ROOT") {
		t.Errorf("podman argv gained YOLO_CTX_ROOT; it mounts at /ctx:\n%s", args)
	}
}
