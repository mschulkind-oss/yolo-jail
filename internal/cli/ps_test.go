package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

func TestPsNoJails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	rc := psRun(psDeps{
		DetectRuntime: func() string { return "podman" },
		RunCmd:        func([]string) (string, bool) { return "", true },
		PathIsDir:     func(string) bool { return true },
		Out:           &buf,
	})
	if rc != 0 {
		t.Errorf("rc = %d", rc)
	}
	if buf.String() != "No running jails.\n" {
		t.Errorf("output = %q", buf.String())
	}
}

func TestPsTableAndProblems(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Seed a tracking file so workspace resolves without an inspect call.
	must(t, runtime.WriteContainerTracking("yolo-a-1111", "/exists"))
	must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))

	psOut := "yolo-a-1111\tUp 2 hours\t2 hours ago\n" +
		"yolo-b-2222\tUp 5 minutes\t5 minutes ago\n"
	deps := psDeps{
		DetectRuntime: func() string { return "podman" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return psOut, true
			}
			if len(argv) >= 2 && argv[1] == "top" {
				// yolo-a healthy (has a user proc); yolo-b n/a (workspace gone
				// short-circuits before top).
				return "COMMAND\nbash\nclaude\n", true
			}
			return "", true
		},
		PathIsDir: func(path string) bool { return path == "/exists" },
		Out:       &bytes.Buffer{},
	}
	var buf bytes.Buffer
	deps.Out = &buf
	psRun(deps)
	out := buf.String()
	// Table header + both rows present.
	if !strings.Contains(out, "CONTAINER") || !strings.Contains(out, "yolo-a-1111") || !strings.Contains(out, "yolo-b-2222") {
		t.Errorf("table missing rows:\n%s", out)
	}
	// yolo-b flagged workspace-gone.
	if !strings.Contains(out, "1 problem jail(s)") || !strings.Contains(out, "yolo-b-2222  (workspace gone)") {
		t.Errorf("problem section wrong:\n%s", out)
	}
	if !strings.Contains(out, "Run 'yolo doctor' to clean up") {
		t.Errorf("missing doctor hint:\n%s", out)
	}
}

func TestPsPrunesStaleTracking(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	must(t, runtime.WriteContainerTracking("yolo-live", "/ws"))
	must(t, runtime.WriteContainerTracking("yolo-dead", "/ws2"))

	deps := psDeps{
		DetectRuntime: func() string { return "podman" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[1] == "ps" {
				return "yolo-live\tUp\tnow\n", true
			}
			return "", true
		},
		PathIsDir: func(string) bool { return true },
		Out:       &bytes.Buffer{},
	}
	psRun(deps)
	// The dead tracking file must be gone; the live one kept.
	if _, ok := runtime.ReadContainerWorkspace("yolo-dead"); ok {
		t.Error("stale tracking file should be pruned")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live"); !ok {
		t.Error("live tracking file should survive")
	}
	_ = paths.ContainerDir()
}

// TestPsContainerRuntimeKeepsLiveTracking documents the end state the unified
// resolver enables on an Apple Container host (the destructive §B/D11 bug): with
// the runtime resolved to "container", ps enumerates via `container ls` and the
// stale-tracking prune keeps the live jail's file while dropping the dead one —
// rather than the old config-blind "podman" pick that saw nothing and wiped both.
func TestPsContainerRuntimeKeepsLiveTracking(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	must(t, runtime.WriteContainerTracking("yolo-live", "/ws"))
	must(t, runtime.WriteContainerTracking("yolo-dead", "/ws2"))

	deps := psDeps{
		DetectRuntime: func() string { return "container" },
		RunCmd: func(argv []string) (string, bool) {
			if len(argv) >= 2 && argv[0] == "container" && argv[1] == "ls" {
				return "ID  IMAGE  STATE\nyolo-live img running\n", true
			}
			return "", true
		},
		PathIsDir: func(string) bool { return true },
		Out:       &bytes.Buffer{},
	}
	psRun(deps)
	if _, ok := runtime.ReadContainerWorkspace("yolo-dead"); ok {
		t.Error("dead tracking file should be pruned on the container runtime")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live"); !ok {
		t.Error("live AC jail's tracking file must survive")
	}
}

// TestPsEnumerationFailureDoesNotPrune is the audit §D11 regression: when the
// runtime probe FAILS (ok=false — e.g. `podman ps` on a macOS host running only
// Apple Container), ps must NOT prune tracking files (they belong to live jails
// the failed probe couldn't see) and must NOT print the misleading "No running
// jails."
func TestPsEnumerationFailureDoesNotPrune(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	must(t, runtime.WriteContainerTracking("yolo-live-1", "/ws1"))
	must(t, runtime.WriteContainerTracking("yolo-live-2", "/ws2"))

	var buf bytes.Buffer
	psRun(psDeps{
		DetectRuntime: func() string { return "podman" },
		// Probe fails to enumerate (ok=false).
		RunCmd:    func([]string) (string, bool) { return "", false },
		PathIsDir: func(string) bool { return true },
		Out:       &buf,
	})
	// Both tracking files MUST survive — pruning them would orphan live jails.
	if _, ok := runtime.ReadContainerWorkspace("yolo-live-1"); !ok {
		t.Error("D11 regression: tracking file deleted on enumeration failure")
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-live-2"); !ok {
		t.Error("D11 regression: tracking file deleted on enumeration failure")
	}
	if strings.Contains(buf.String(), "No running jails.") {
		t.Errorf("must not claim 'No running jails' on a failed probe: %q", buf.String())
	}
}

// TestDetectListingRuntimeHonorsConfig covers audit finding 5: `yolo ps` (and
// prune) never consulted the workspace `runtime` key. detectListingRuntime now
// loads the config and feeds runtime.ResolveRuntime, so a workspace pinned to
// Apple Container resolves to "container" even on the Linux test host (config
// precedence wins before the platform branch). RED before the commands.go wiring
// (detectListingRuntime did not exist and ps loaded no config).
func TestDetectListingRuntimeHonorsConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_RUNTIME", "")
	os.Unsetenv("YOLO_RUNTIME")
	// `macos-user` rather than `container` on purpose. The probe can return ONLY podman or
	// container, so pinning to container made this assertion VACUOUS wherever the platform
	// default already was container — verified on a macOS host with Apple Container
	// installed, where deleting the config lookup outright still passed. macos-user is in
	// AllRuntimes but is never auto-detected on any platform, so the assertion can only be
	// satisfied by config precedence actually working.
	ws := t.TempDir()
	if err := os.WriteFile(ws+"/yolo-jail.jsonc", []byte(`{"runtime":"macos-user"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectListingRuntime(ws); got != "macos-user" {
		t.Errorf("detectListingRuntime honoring config = %q, want macos-user", got)
	}

	// A workspace with no config and no override → whatever the PLATFORM PROBE says.
	//
	// Asserted against the probe rather than the literal "podman": this test ran on a
	// Linux host for its whole life, and hardcoding the Linux answer made it fail on
	// macOS — where the correct result is "container" if Apple Container is installed —
	// reporting a broken resolver when the resolver was right. The platform matrix
	// itself is not this test's job and is already pinned exhaustively by
	// runtime.TestResolveRuntimeConfigAware; what belongs HERE is that a config-less
	// workspace reaches the probe at all instead of the previous line's "container"
	// leaking across, which is the wiring audit finding 5 was about.
	empty := t.TempDir()
	want := runtime.ResolveRuntime("", "", paths.IsMacOS, func(bin string) bool {
		_, err := exec.LookPath(bin)
		return err == nil
	})
	if got := detectListingRuntime(empty); got != want {
		t.Errorf("detectListingRuntime with no config = %q, want the platform probe's %q", got, want)
	}
}

// TestPsColorParity locks the additive-color contract: with Color=false the
// output is byte-identical to the pre-change raw-fmt bytes (no ESC anywhere),
// and with Color=true the idle / problem / doctor-tip framing lines carry ANSI.
func TestPsColorParity(t *testing.T) {
	t.Run("idle plain is byte-exact", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		var buf bytes.Buffer
		psRun(psDeps{
			DetectRuntime: func() string { return "podman" },
			RunCmd:        func([]string) (string, bool) { return "", true },
			PathIsDir:     func(string) bool { return true },
			Out:           &buf,
			Color:         false,
		})
		if got := buf.String(); got != "No running jails.\n" {
			t.Errorf("plain idle output = %q, want the pre-change bytes", got)
		}
	})

	t.Run("idle color emits ANSI", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		var buf bytes.Buffer
		psRun(psDeps{
			DetectRuntime: func() string { return "podman" },
			RunCmd:        func([]string) (string, bool) { return "", true },
			PathIsDir:     func(string) bool { return true },
			Out:           &buf,
			Color:         true,
		})
		got := buf.String()
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("colored idle output has no ANSI: %q", got)
		}
		if !strings.Contains(got, "No running jails.") {
			t.Errorf("colored idle output lost its literal text: %q", got)
		}
	})

	t.Run("problems: plain byte-exact, color has ANSI", func(t *testing.T) {
		mkDeps := func(out *bytes.Buffer, color bool) psDeps {
			return psDeps{
				DetectRuntime: func() string { return "podman" },
				RunCmd: func(argv []string) (string, bool) {
					if len(argv) >= 2 && argv[1] == "ps" {
						return "yolo-b-2222\tUp 5 minutes\t5 minutes ago\n", true
					}
					return "", true
				},
				PathIsDir: func(string) bool { return false }, // workspace gone
				Out:       out,
				Color:     color,
			}
		}

		home := t.TempDir()
		t.Setenv("HOME", home)
		must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))
		var plain bytes.Buffer
		psRun(mkDeps(&plain, false))
		if strings.Contains(plain.String(), "\x1b[") {
			t.Errorf("plain problem output leaked ANSI: %q", plain.String())
		}
		// The stripped bytes match the current raw-fmt rendering exactly.
		wantTail := "\n⚠  1 problem jail(s):\n" +
			"  yolo-b-2222  (workspace gone)\n" +
			"\n  Run 'yolo doctor' to clean up\n"
		if !strings.HasSuffix(plain.String(), wantTail) {
			t.Errorf("plain problem tail = %q, want suffix %q", plain.String(), wantTail)
		}

		home2 := t.TempDir()
		t.Setenv("HOME", home2)
		must(t, runtime.WriteContainerTracking("yolo-b-2222", "/gone"))
		var col bytes.Buffer
		psRun(mkDeps(&col, true))
		if !strings.Contains(col.String(), "\x1b[") {
			t.Errorf("colored problem output has no ANSI: %q", col.String())
		}
		// Stripping the color must reproduce the plain bytes exactly.
		if got := stripANSI(col.String()); got != plain.String() {
			t.Errorf("color-stripped != plain:\n color=%q\n plain=%q", got, plain.String())
		}
	})
}

// stripANSI removes CSI SGR escape sequences for the parity assertion.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// redirectSessionBase points the host-services base this package's TestMain isolated at a dir of
// the test's own, so the sessions it plants are the only ones the listing can find.
func redirectSessionBase(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = base
	t.Cleanup(func() { paths.HostSingletonDir = prev })
	return paths.HostServicesBase(false)
}

// plantPsSession makes a host-services session dir the way the launch's openServicesSession
// does: "live" holds its lock on a descriptor of the test's own, "gone" leaves it free, and
// "nolock" makes no lock file (a session starting up). rec nil writes no record (an older yolo).
func plantPsSession(t *testing.T, base, cname, state string, rec *runtime.SessionRecord) string {
	t.Helper()
	dir, err := os.MkdirTemp(base, paths.HostServicesSessionPrefix(cname))
	must(t, err)
	if rec != nil {
		must(t, runtime.WriteSessionRecord(dir, *rec))
	}
	if state == "nolock" {
		return dir
	}
	f, err := os.OpenFile(filepath.Join(dir, paths.HostServicesSessionLockName), os.O_CREATE|os.O_RDWR, 0o600)
	must(t, err)
	if state == "gone" {
		_ = f.Close()
		return dir
	}
	must(t, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB))
	t.Cleanup(func() { _ = f.Close() })
	return dir
}

// noExecDeps is psDeps for the macos-user runtime with a RunCmd that fails the test: macos-user
// names no binary, and the exec the container branch makes is the defect this replaced.
func noExecDeps(t *testing.T, out *bytes.Buffer, format string) psDeps {
	return psDeps{
		DetectRuntime: func() string { return "macos-user" },
		RunCmd: func(argv []string) (string, bool) {
			t.Fatalf("yolo ps on macos-user ran %v; it has no runtime binary to ask", argv)
			return "", false
		},
		PathIsDir: func(string) bool { return true },
		Out:       out,
		Format:    format,
	}
}

// TestPsOnMacosUserListsItsLiveSessions: `yolo ps` with runtime macos-user used to exec
// `macos-user ps` and print a red "Could not query". It now lists the backend's running sessions
// from their own locks and records: a live one with its workspace, one with no lock yet as
// "starting or unknown", and neither an ended one nor a `yolo host` launch's. It runs nothing, and
// it leaves every container jail's tracking file alone, since an Apple Container jail on the same
// Mac is not in a session listing and pruning on one would delete its file (D11).
func TestPsOnMacosUserListsItsLiveSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := redirectSessionBase(t)
	must(t, runtime.WriteContainerTracking("yolo-ac-1234", "/ac-workspace"))

	plantPsSession(t, base, "yolo-live-1", "live",
		&runtime.SessionRecord{Notch: runtime.NotchMacosUser, Workspace: "/Users/Shared/yolo/live", Name: "yolo-live-1"})
	plantPsSession(t, base, "yolo-ended-2", "gone",
		&runtime.SessionRecord{Notch: runtime.NotchMacosUser, Workspace: "/Users/Shared/yolo/ended", Name: "yolo-ended-2"})
	plantPsSession(t, base, "yolo-hostnotch-3", "live",
		&runtime.SessionRecord{Notch: runtime.NotchHost, Workspace: "/Users/me/proj", Name: "yolo-hostnotch-3"})
	starting := plantPsSession(t, base, "yolo-starting-4", "nolock", nil)

	var buf bytes.Buffer
	if rc := psRun(noExecDeps(t, &buf, "")); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	out := buf.String()
	for _, want := range []string{
		"SESSION", "yolo-live-1", "running (macos-user session)", "/Users/Shared/yolo/live",
		filepath.Base(starting), "starting or unknown",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the listing lacks %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"yolo-ended-2", "yolo-hostnotch-3", "Could not query", "No running"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("the listing shows %q:\n%s", unwanted, out)
		}
	}
	if _, ok := runtime.ReadContainerWorkspace("yolo-ac-1234"); !ok {
		t.Error("yolo ps on macos-user deleted a container jail's tracking file (D11)")
	}

	var js bytes.Buffer
	psRun(noExecDeps(t, &js, "json"))
	var rep psReport
	if err := json.Unmarshal(js.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, js.String())
	}
	if !rep.Enumerated || rep.Runtime != "macos-user" || len(rep.Jails) != 2 {
		t.Errorf("JSON report = %+v, want enumerated macos-user with the live and the starting session", rep)
	}
}

// TestPsOnMacosUserWithNoSessionsSaysSoAndWhereContainersAre: an empty listing is an answer
// (enumerated), and it names where a container jail on the same Mac is listed instead.
func TestPsOnMacosUserWithNoSessionsSaysSoAndWhereContainersAre(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	redirectSessionBase(t)
	var buf bytes.Buffer
	psRun(noExecDeps(t, &buf, ""))
	want := "No running macos-user sessions.\n" +
		"Container jails are listed under their own runtime: YOLO_RUNTIME=container yolo ps " +
		"(or YOLO_RUNTIME=podman yolo ps).\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
	var js bytes.Buffer
	psRun(noExecDeps(t, &js, "json"))
	var rep psReport
	if err := json.Unmarshal(js.Bytes(), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, js.String())
	}
	if !rep.Enumerated || len(rep.Jails) != 0 {
		t.Errorf("JSON report = %+v, want enumerated with no jails", rep)
	}
}

// TestPsOnMacosUserLeavesOutAnotherAccountsSessions: the session base is the machine-wide /tmp,
// so on a Mac with more than one user another account's session dirs sit beside this one's.
// `yolo ps` lists this user's alone; listed, another account's would be a row named after its
// dir. A dir another account owns needs root to make, so this runs only as root (the runtime
// package's owner-seam test covers the filter everywhere).
func TestPsOnMacosUserLeavesOutAnotherAccountsSessions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to make a session dir another account owns")
	}
	t.Setenv("HOME", t.TempDir())
	base := redirectSessionBase(t)
	theirs := plantPsSession(t, base, "yolo-theirs-1", "live",
		&runtime.SessionRecord{Notch: runtime.NotchMacosUser, Workspace: "/Users/them/proj", Name: "yolo-theirs-1"})
	must(t, os.Lchown(theirs, 65534, 65534))

	var buf bytes.Buffer
	if rc := psRun(noExecDeps(t, &buf, "")); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if out := buf.String(); strings.Contains(out, "yolo-theirs-1") || strings.Contains(out, filepath.Base(theirs)) ||
		!strings.Contains(out, "No running macos-user sessions.") {
		t.Errorf("yolo ps listed another account's session:\n%s", out)
	}
}
