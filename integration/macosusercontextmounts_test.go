package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// CONTEXT MOUNTS ON A REAL macos-user LAUNCH (docs/design/context-mounts.md §4 steps 4-5): the
// probes §4 lists that need the sandbox ACCOUNT, which macosuserseatbelt_test.go's policy suite
// does not have — the DAC preflight asked as _yolojail, a launch that delivers a read-only and
// a read-write mount through $YOLO_CONTEXT_DIR, the root-owned link the agent cannot replace,
// and the two measurements OQ-CX8 (CX-D5) waits on. All behind requireMacosUser, so they run on
// the scheduled macos-user.yml job and skip everywhere else.
//
// ⚠ NONE OF THIS HAS RUN YET. It was written on Linux, against code whose every decision is
// unit-tested there; what the kernel answers is what the first scheduled run will say.

// ctxSourceUnder makes a context-mount source directory under parent, mode `mode`, holding a
// seed file, and removes it when the test ends — with sudo when the sandbox wrote into it, for
// removeMacosUserWorkspace's reason. It returns the resolved path.
func ctxSourceUnder(t *testing.T, parent string, mode os.FileMode) string {
	t.Helper()
	dir, err := os.MkdirTemp(parent, "yolo-it-ctx-")
	if err != nil {
		t.Fatalf("creating a context-mount source under %s: %v", parent, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resolved, "seed"), []byte("CTX_SEED\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(resolved, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(resolved); err == nil {
			return
		}
		if !strings.HasPrefix(filepath.Base(resolved), "yolo-it-ctx-") || strings.Contains(resolved, "..") {
			t.Fatalf("refusing to escalate removal of %s: not a fixture", resolved)
		}
		if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", resolved) {
			t.Logf("could not remove %s even with sudo; remove it by hand", resolved)
		}
	})
	return resolved
}

// runPreflight runs one DAC preflight argv with `sudo -n` (the gate proved it cannot prompt)
// and reports whether the kernel granted the access.
func runPreflight(t *testing.T, argv []string) bool {
	t.Helper()
	if len(argv) == 0 || argv[0] != "sudo" {
		t.Fatalf("a preflight argv that does not start with sudo: %v", argv)
	}
	full := append([]string{"sudo", "-n"}, argv[1:]...)
	return runQuiet(30*time.Second, full...)
}

// THE DAC PREFLIGHT, asked of the kernel as the sandbox account (§3.5): a source the sandbox
// cannot read — 0700, owned by the invoking user, outside the shared root's inherited ACL —
// fails its read probe; the same folder at 0755 passes every probe. A read-only source is
// sited under /Users/Shared and NOT under the shared root on purpose: there an inherited group
// ACE would admit the sandbox whatever the mode, which is the point of the shared root and
// would make this control say nothing about the mode bits.
func TestMacosUserContextPreflightRefusesASourceTheSandboxCannotRead(t *testing.T) {
	requireMacosUser(t)
	closed := ctxSourceUnder(t, sharedUsersDir, 0o700)
	open := ctxSourceUnder(t, sharedUsersDir, 0o755)

	for _, p := range macosuser.ContextPreflight([]macosuser.ContextLink{{Dest: "/ctx/open", Source: open, Dir: true}}, "") {
		if !runPreflight(t, p.Argv) {
			t.Errorf("the CONTROL failed: %s cannot %s %s at 0755, so a refusal below would "+
				"say nothing about the mode (%v)", macosuser.SandboxUser, p.Access, open, p.Argv)
		}
	}
	probes := macosuser.ContextPreflight([]macosuser.ContextLink{{Dest: "/ctx/closed", Source: closed, Dir: true}}, "")
	if runPreflight(t, probes[0].Argv) {
		t.Errorf("%s can read %s at 0700, owned by another account: the DAC preflight would "+
			"admit a source the sandbox should not reach (%v)", macosuser.SandboxUser, closed, probes[0].Argv)
	}
}

// THE DELIVERY, END TO END: a launch with a read-only mount on neutral ground and a read-write
// one under the shared root, both in the user config, reads and writes them through
// $YOLO_CONTEXT_DIR as the sandbox account, under the session profile — and cannot write the
// read-only one, re-point its link, or walk out of a source. Each line of the probe is one
// operation in a subshell, so a refusal is a result rather than an exit.
func TestMacosUserDeliversContextMountsThroughTheContextDir(t *testing.T) {
	requireMacosUser(t)
	ro := ctxSourceUnder(t, sharedUsersDir, 0o755)
	rw := ctxSourceUnder(t, macosuser.SharedRootDefault(), 0o755)
	sibling := ctxSourceUnder(t, sharedUsersDir, 0o755)
	mounts, err := json.Marshal([]any{
		ro + ":/ctx/lib",
		map[string]string{"host": rw, "at": "/ctx/scratch", "mode": "rw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"mounts": `+string(mounts)+`}`)
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, macosUserContextProbe(sibling))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END CTX ===") {
		t.Fatalf("the launch did not run its context probe (rc %d).\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "CTX")
	for key, want := range map[string]string{
		"ro-read":      "CTX_SEED",
		"ro-write":     "DENIED",
		"rw-read":      "CTX_SEED",
		"rw-write":     "ALLOWED",
		"link-replace": "DENIED",
		"walk-out":     "DENIED",
	} {
		if got[key] != want {
			t.Errorf("%s|%s, want %s.\nfull output:\n%s", key, got[key], want, r.stdout)
		}
	}
	if !strings.HasPrefix(got["ctx-dir"], "/var/yolo-jail/ctx/") {
		t.Errorf("$YOLO_CONTEXT_DIR is %q, want the root-owned context dir", got["ctx-dir"])
	}
	if !strings.Contains(r.combined(), "Read-write mount:") {
		t.Errorf("the read-write mount was not disclosed on the launch stream:\n%s", r.combined())
	}
}

// macosUserContextProbe is the sandboxed script for the test above.
func macosUserContextProbe(sibling string) string {
	sib := "'" + strings.ReplaceAll(filepath.Base(sibling), "'", `'\''`) + "'"
	return strings.Join([]string{
		`echo "=== CTX ==="`,
		`d="$YOLO_CONTEXT_DIR"`,
		`echo "ctx-dir|$d"`,
		`echo "ro-read|$(cat "$d/lib/seed" 2>/dev/null || echo DENIED)"`,
		`if ( touch "$d/lib/yolo-it-probe" ) 2>/dev/null; then rm -f "$d/lib/yolo-it-probe"; echo "ro-write|ALLOWED"; else echo "ro-write|DENIED"; fi`,
		`echo "rw-read|$(cat "$d/scratch/seed" 2>/dev/null || echo DENIED)"`,
		`if ( touch "$d/scratch/yolo-it-probe" && rm "$d/scratch/yolo-it-probe" ) 2>/dev/null; then echo "rw-write|ALLOWED"; else echo "rw-write|DENIED"; fi`,
		`if ( ln -sfn /tmp "$d/lib" ) 2>/dev/null; then echo "link-replace|ALLOWED"; else echo "link-replace|DENIED"; fi`,
		`if ( cat "$d/lib/../` + sib + `/seed" ) >/dev/null 2>&1; then echo "walk-out|ALLOWED"; else echo "walk-out|DENIED"; fi`,
		`echo "=== END CTX ==="`,
	}, "\n")
}

// OQ-CX8's TWO UNMEASURED FACTS (CX-D5), RECORDED, never asserted: whether the sandbox account
// gets through to a source on another volume, and to a privacy-guarded (TCC) folder of the
// invoking user, first at the POSIX layer (the DAC preflight's own probe) and then under a
// profile that grants the source. v1 refuses both whatever this says; the log is what a
// ruling to deliver either would be made from.
func TestMacosUserContextVolumesAndPrivacyDirsMeasurement(t *testing.T) {
	requireMacosUser(t)
	var candidates []string
	if entries, err := os.ReadDir("/Volumes"); err == nil {
		for _, e := range entries {
			if e.Name() != filepath.Base("/Volumes/Macintosh HD") {
				candidates = append(candidates, filepath.Join("/Volumes", e.Name()))
			}
		}
	}
	if u, err := user.Current(); err == nil {
		for _, d := range []string{"Desktop", "Documents", "Downloads"} {
			candidates = append(candidates, filepath.Join(u.HomeDir, d))
		}
	}
	if len(candidates) == 0 {
		t.Skip("no other volume and no privacy-guarded folder on this machine to measure")
	}
	// The profile must be readable by the sandbox account, which a per-user temp dir is not.
	profDir, err := os.MkdirTemp("/private/tmp", "yolo-it-ctx-prof-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(profDir) })
	_ = os.Chmod(profDir, 0o755)
	for _, c := range candidates {
		src, err := filepath.EvalSymlinks(c)
		if err != nil {
			t.Logf("MEASUREMENT %s: does not resolve (%v)", c, err)
			continue
		}
		link := macosuser.ContextLink{Dest: "/ctx/probe", Source: src, Dir: true}
		dac := runPreflight(t, macosuser.ContextPreflight([]macosuser.ContextLink{link}, "")[0].Argv)
		profile := filepath.Join(profDir, "p.sb")
		if err := os.WriteFile(profile, []byte(macosuser.SeatbeltProfileWithContext(
			macosuser.SharedRootDefault(), "", nil, macosuser.HomeReadonly{},
			[]macosuser.ContextLink{link}, nil, "off")), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "sudo", "-n", "--user="+macosuser.SandboxUser,
			"/usr/bin/sandbox-exec", "-f", profile, "/bin/ls", src).CombinedOutput()
		cancel()
		t.Logf("MEASUREMENT %s (resolved %s): DAC read as %s = %v; listing under a profile "+
			"granting it: err=%v\n%s", c, src, macosuser.SandboxUser, dac, err, out)
	}
}

// A PACK'S SINGLE-FILE `mount` IS COPIED INTO THE CONTEXT DIR (context-mounts.md CX-D23): from the
// user's home, which no link reaches on this backend, to the grant's own path under
// $YOLO_CONTEXT_DIR, root-owned. The sandbox reads it and cannot write, replace or delete it —
// the root-owned tree outside the profile's writable set is the read-only half, with no rule of
// its own.
func TestMacosUserCopiesAPackFileMountIntoTheContextDir(t *testing.T) {
	requireMacosUser(t)
	nonce := acParityNonce()
	pack := filepath.Join(t.TempDir(), "yolo-it-filemount")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name": "yolo-it-filemount", `+
		`"contributes": [{"kind": "mount", "host": "yolo-it-filemount.txt", "into": "filemount/notes.txt"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "yolo-it-filemount"}]}`)
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), "yolo-it-filemount.txt"),
		[]byte("FILEMOUNT-"+nonce+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := macosUserWorkspace(t, `{}`)

	r := runMacosUser(t, ws, strings.Join([]string{
		`echo "=== CTX ==="`,
		`f="$YOLO_CONTEXT_DIR/filemount/notes.txt"`,
		`echo "read|$(cat "$f" 2>/dev/null || echo DENIED)"`,
		`if ( echo x >> "$f" ) 2>/dev/null; then echo "write|ALLOWED"; else echo "write|DENIED"; fi`,
		`if ( echo y > /tmp/yolo-it-fm && mv -f /tmp/yolo-it-fm "$f" ) 2>/dev/null; then echo "replace|ALLOWED"; else echo "replace|DENIED"; fi`,
		`if ( rm -f "$f" && [ ! -e "$f" ] ) 2>/dev/null; then echo "delete|ALLOWED"; else echo "delete|DENIED"; fi`,
		`echo "=== END CTX ==="`,
	}, "\n"))
	if r.rc != 0 || !strings.Contains(r.stdout, "=== END CTX ===") {
		t.Fatalf("the launch did not run its probe (rc %d).\nstdout:\n%s\nstderr:\n%s", r.rc, r.stdout, r.stderr)
	}
	got := macosUserHomeProbeFields(t, r.stdout, "CTX")
	for key, want := range map[string]string{
		"read":    "FILEMOUNT-" + nonce,
		"write":   "DENIED",
		"replace": "DENIED",
		"delete":  "DENIED",
	} {
		if got[key] != want {
			t.Errorf("%s|%s, want %s.\nfull output:\n%s", key, got[key], want, r.stdout)
		}
	}
	if strings.Contains(r.combined(), "Refusing the macos-user launch") {
		t.Errorf("a single-file pack mount refused the launch:\n%s", r.combined())
	}
}
