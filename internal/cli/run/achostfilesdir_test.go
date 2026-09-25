package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// A DIRECTORY-SHAPED host_files SOURCE ON AN APPLE CONTAINER THAT IGNORES `:ro`.
//
// The other `/ctx` read-only binds that consult roBindsUnsupported — config `mounts`, pack
// `mount` grants, the host nvim config, the captures store — ask it first and, below
// acROBindsFloor, refuses the mount with a printed reason. The directory branch of
// hostUserFileArgs did not: it bound the user's host directory `:ro` on every Apple
// Container version, so below 1.1.0 the jail held WRITE access to a host tree the user
// declared read-only, and nothing said so (userguide/reference/settings-per-setup.md, the
// `acdir` footnote).
//
// Declining is safe for the jail's side: the entrypoint's stageHostFile treats an absent
// /ctx/host-user/<slug> directory as "nothing to copy" (fail-open, the same as a host
// source the user has not created yet), so no empty or masking destination is written.

// acDirHostFileFixture is one directory-shaped host_files entry whose source exists.
func acDirHostFileFixture(t *testing.T) (config.HostFileEntry, string) {
	t.Helper()
	home := t.TempDir()
	dirSrc := filepath.Join(home, "certs")
	if err := os.MkdirAll(dirSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	return config.HostFileEntry{
		Path: "certs", Source: dirSrc, IsDir: true, Mode: config.HostFileModeReadonly,
	}, home
}

func TestHostFileDirSourceDeclinedWhereACIgnoresReadOnly(t *testing.T) {
	cases := []struct {
		name    string
		version string // `container --version` output; "" = no container on PATH
	}{
		{"AC below the floor ignores :ro", "container CLI version 1.0.9 (build: release)"},
		{"an unreadable AC version fails closed", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, home := acDirHostFileFixture(t)
			in := hostFileIn(t, entry)
			in.rt = "container"

			o := goldenOptions("/ws", home)
			ac, _ := acOptions(tc.version)
			o.LookPath, o.Exec = ac.LookPath, ac.Exec
			var stdout bytes.Buffer
			o.Stdout = &stdout

			args := strings.Join(o.hostUserFileArgs(in), " ")
			if strings.Contains(args, entry.Source+":") {
				t.Errorf("a directory host_files source was bound on an Apple Container that "+
					"cannot honor :ro, so the user's host tree is writable from the jail:\n%s", args)
			}
			msg := stdout.String()
			if !strings.Contains(msg, "Skipping host_files") || !strings.Contains(msg, "~/certs") {
				t.Errorf("the decline is silent or does not name the entry:\n%s", msg)
			}
			if reason := o.roBindsUnsupported("container"); !strings.Contains(msg, reason) {
				t.Errorf("the decline does not carry roBindsUnsupported's reason %q:\n%s", reason, msg)
			}
		})
	}
}

// The other half of the floor: a version that honors `:ro` keeps the bind, and podman —
// which always honored it — is untouched. A fix that simply stopped binding directories on
// Apple Container would fail here.
func TestHostFileDirSourceStillBoundWhereReadOnlyIsHonored(t *testing.T) {
	for _, tc := range []struct{ rt, version string }{
		{"container", "container CLI version 1.1.0 (build: release)"},
		{"podman", ""},
	} {
		t.Run(tc.rt, func(t *testing.T) {
			entry, home := acDirHostFileFixture(t)
			in := hostFileIn(t, entry)
			in.rt = tc.rt

			o := goldenOptions("/ws", home)
			ac, _ := acOptions(tc.version)
			o.LookPath, o.Exec = ac.LookPath, ac.Exec
			var stdout bytes.Buffer
			o.Stdout = &stdout

			args := strings.Join(o.hostUserFileArgs(in), " ")
			want := entry.Source + ":" + hostUserCtxDir + "/" + entry.Slug() + ":ro"
			if !strings.Contains(args, want) {
				t.Errorf("the directory bind was dropped where :ro is honored; want %q in:\n%s", want, args)
			}
			if strings.Contains(stdout.String(), "Skipping host_files") {
				t.Errorf("a decline was printed where :ro is honored:\n%s", stdout.String())
			}
		})
	}
}

// THE CALL SITE, not just the emitter: the full Apple Container argv below the floor must
// carry no bind of the directory source. Fails if assembleRunCmd reaches the directory
// through any path that skips the version gate.
func TestAssembledACArgvCarriesNoWritableHostFileDir(t *testing.T) {
	entry, home := acDirHostFileFixture(t)
	t.Setenv("HOME", home)
	ws := t.TempDir()
	emptyLoopholeDirs(t)

	wsState := filepath.Join(ws, ".yolo", "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false
	ac, _ := acOptions("container CLI version 0.12.3 (build: release)")
	o.LookPath, o.Exec = ac.LookPath, ac.Exec
	o.Stdout = &bytes.Buffer{}
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})

	argv := o.assembleRunCmd(&assembleInput{
		cfg: newConfig("security", sec), rt: "container", cname: "yolo-ws-abcd1234",
		hostFiles:  []config.HostFileEntry{entry},
		agentsPath: filepath.Join(ws, "agents"), wsState: wsState,
		miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	for _, a := range argv {
		if strings.HasPrefix(a, entry.Source+":") {
			t.Fatalf("the assembled Apple Container 0.12.3 argv binds the host_files directory "+
				"source %q, which that version makes writable:\n%s", a, strings.Join(argv, " "))
		}
	}
}

// The macos-user warning for a DIRECTORY host_files source points the user at Apple
// Container, and must not promise a bind that runtime now declines below acROBindsFloor:
// "which binds it" unqualified sent a user on AC 1.0.x from one skipped entry to another.
func TestMacosUserDirHostFileWarningQualifiesTheAppleContainerFloor(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/big/", "source": "~/big/"}]`)
	if err := os.MkdirAll(filepath.Join(home, "big"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHostFileAt(t, filepath.Join(home, "big", "a.txt"), "x\n", 0o644)

	_, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)
	flat := strings.Join(strings.Fields(out), " ")
	if !strings.Contains(flat, "does not cross on macos-user") {
		t.Fatalf("the launch did not print the directory host_files warning:\n%s", out)
	}
	if !strings.Contains(flat, "binds it read-only from Apple Container "+acROBindsFloor) {
		t.Errorf("the warning recommends Apple Container without naming the %s floor below "+
			"which it skips a directory source:\n%s", acROBindsFloor, out)
	}
}
