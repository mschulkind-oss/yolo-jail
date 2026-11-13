package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// psNow is the clock every fixture reads ages against.
var psNow = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

// psFake is a scripted machine for psRun: what each runtime's listing answers, and every argv
// asked. A source whose listing is in block waits for its budget to expire (the event the
// timed-out state is: the context's deadline, SUP-D24), never for a wall-clock sleep.
type psFake struct {
	mu        sync.Mutex
	calls     [][]string
	podman    string
	podmanErr error
	ac        string
	acErr     error
	block     map[string]bool
	top       map[string]string
}

func (f *psFake) run(ctx context.Context, argv []string) (string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, argv)
	f.mu.Unlock()
	if f.block[argv[0]] && len(argv) > 1 && (argv[1] == "ps" || argv[1] == "ls") {
		<-ctx.Done()
		return "", ctx.Err()
	}
	switch {
	case argv[0] == "podman" && argv[1] == "ps":
		return f.podman, f.podmanErr
	case argv[0] == "container" && argv[1] == "ls":
		return f.ac, f.acErr
	case argv[0] == "podman" && argv[1] == "top":
		if out, ok := f.top[argv[2]]; ok {
			return out, nil
		}
		return "COMMAND\nbash\nclaude\n", nil
	}
	return "", errors.New("not scripted")
}

func (f *psFake) asked(bin string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c[0] == bin {
			return true
		}
	}
	return false
}

// psFixture is psDeps for goos with the named binaries installed, reading f, with sessions under
// a base of the test's own and HOME a fresh dir (so its launch records are the test's alone).
func psFixture(t *testing.T, goos string, f *psFake, bins ...string) (psDeps, *bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := t.TempDir()
	out := &bytes.Buffer{}
	return psDeps{
		GOOS: goos,
		HasBinary: func(bin string) bool {
			for _, b := range bins {
				if b == bin {
					return true
				}
			}
			return false
		},
		RunCmd:      f.run,
		PathIsDir:   func(string) bool { return true },
		SessionBase: base,
		Home:        "/home/me",
		Now:         func() time.Time { return psNow },
		Out:         out,
	}, out, base
}

// psJSON runs deps with --format json and decodes the document.
func psJSON(t *testing.T, deps psDeps) psDoc {
	t.Helper()
	var js bytes.Buffer
	deps.Out, deps.Format = &js, "json"
	if rc := psRun(deps); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	var doc psDoc
	if err := json.Unmarshal(js.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, js.String())
	}
	return doc
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

// TestPsOnAMacListsEverySourceInOneList is named-setups.md §12's whole point (OQ-SU6): on a Mac
// every source is asked at once and its rows share one table. Apple Container is unreachable, so
// its line names `container system start`, JSON says complete false, the other three sources'
// rows are listed, and the exit is 0.
func TestPsOnAMacListsEverySourceInOneList(t *testing.T) {
	f := &psFake{acErr: errors.New("container system is not running")}
	deps, out, base := psFixture(t, "darwin", f, "podman", "container")
	podJail := runtime.JailName("/home/me/code/yolo-jail", "podman-self")
	f.podman = podJail + "\tUp 3 hours\t" + "1762938000\n"
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: podJail, Setup: "podman-self",
		Confinement: setup.ConfinementPodman, RunAs: setup.RunAsSelf, Workspace: "/home/me/code/yolo-jail",
		Started: psNow.Add(-3 * time.Hour), Packs: []string{"claude", "codex"}, YoloVersion: "0.17.0+12"}))
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-ac-1", Setup: "apple-container-self",
		Confinement: setup.ConfinementAppleContainer, Workspace: "/home/me/ac"}))
	plantPsSession(t, base, "yolo-site-1-seatbelt-yolo-account", "nolock", nil)
	plantPsSession(t, base, "yolo-yolo-jail-2-seatbelt-self", "live", &runtime.SessionRecord{Notch: runtime.NotchMacosUser,
		Workspace: "/home/me/code/yolo-jail", Name: "yolo-yolo-jail-2-seatbelt-self", Setup: "seatbelt-self",
		Started: psNow.Add(-12 * time.Minute).Format(time.RFC3339)})
	plantPsSession(t, base, "yolo-yolo-jail-2", "live", &runtime.SessionRecord{Notch: runtime.NotchHost,
		Workspace: "/home/me/code/yolo-jail", Name: "yolo-yolo-jail-2", Setup: "host-self", PID: os.Getpid(),
		Started: psNow.Add(-40 * time.Minute).Format(time.RFC3339)})
	plantPsSession(t, base, "yolo-ended-3", "gone", &runtime.SessionRecord{Notch: runtime.NotchMacosUser,
		Workspace: "/home/me/ended", Name: "yolo-ended-3"})

	if rc := psRun(deps); rc != 0 {
		t.Fatalf("rc = %d with an unreachable source; yolo ps reports, it does not judge", rc)
	}
	text := out.String()
	for _, want := range []string{
		"WORKSPACE", "SETUP", "STATUS", "AGE", "PACKS",
		"~/code/yolo-jail", "podman-self", "3h", "claude, codex",
		"seatbelt-self", "12m", "host-self", "40m", "starting",
		"Apple Container could not be asked (container system is not running)",
		"1 jail recorded on Apple Container could not be confirmed", "    container system start",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the listing lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ended") || strings.Contains(text, "No jails running") {
		t.Errorf("the listing shows an ended session or claims no jails:\n%s", text)
	}

	doc := psJSON(t, deps)
	if doc.Schema != "yolo.ps/1" || doc.Frame != "host" || doc.Complete {
		t.Errorf("document head = %q %q complete=%v; want yolo.ps/1, host, false", doc.Schema, doc.Frame, doc.Complete)
	}
	wantStates := []string{"podman:listed", "apple-container:unreachable", "seatbelt:listed", "host:listed"}
	if len(doc.Sources) != len(wantStates) {
		t.Fatalf("sources = %+v, want %v", doc.Sources, wantStates)
	}
	for i, s := range doc.Sources {
		if got := s.Confinement + ":" + s.State; got != wantStates[i] {
			t.Errorf("source %d = %s, want %s", i, got, wantStates[i])
		}
	}
	if ac := doc.Sources[1]; ac.Jails != nil || ac.NextStep != "container system start" || ac.Detail == "" {
		t.Errorf("the unreachable source = %+v; want a detail, its next step and no count", ac)
	}
	if pd := doc.Sources[0]; pd.Jails == nil || *pd.Jails != 1 || pd.Detail != "" {
		t.Errorf("Podman's source = %+v; want a count of 1 and no detail", pd)
	}
	if len(doc.Jails) != 4 {
		t.Fatalf("jails = %+v, want the Podman jail and three sessions", doc.Jails)
	}
	var pod psJailJSON
	for _, j := range doc.Jails {
		if j.Name == podJail {
			pod = j
		}
		if j.Problems == nil {
			t.Errorf("%s's problems is null; it is always an array", j.Name)
		}
	}
	if pod.Setup == nil || *pod.Setup != "podman-self" || pod.Confinement != "podman" || pod.RunAs == nil ||
		*pod.RunAs != "self" || pod.Workspace == nil || *pod.Workspace != "/home/me/code/yolo-jail" ||
		pod.Started == nil || *pod.Started != "2026-11-12T09:00:00Z" || strings.Join(pod.Packs, ",") != "claude,codex" ||
		pod.YoloVersion == nil || *pod.YoloVersion != "0.17.0+12" || pod.Status != "running" {
		t.Errorf("the Podman row = %+v", pod)
	}
}

// TestPsATimedOutSourceIsTimedOutWhileTheOthersList: a source that does not answer within its
// budget is timed-out, its jails unknown, while every other source's rows are listed (SU-D9). The
// budget is a dep, so the test waits on the context's deadline and nothing else.
func TestPsATimedOutSourceIsTimedOutWhileTheOthersList(t *testing.T) {
	f := &psFake{block: map[string]bool{"container": true}, podman: "yolo-a-1\tUp\t1762938000\n"}
	deps, out, _ := psFixture(t, "darwin", f, "podman", "container")
	deps.SourceBudget = time.Millisecond
	psRun(deps)
	if !strings.Contains(out.String(), "Apple Container is installed but not answering (no answer within 1ms)") ||
		!strings.Contains(out.String(), "running") {
		t.Errorf("listing:\n%s", out.String())
	}
	doc := psJSON(t, deps)
	if doc.Complete || doc.Sources[1].State != "timed-out" || doc.Sources[0].State != "listed" || len(doc.Jails) != 1 {
		t.Errorf("document = %+v; want Apple Container timed-out, Podman's row listed, incomplete", doc)
	}
}

// TestPsAnUnknownSetupIsAQuestionMarkAndAFootnote: a jail an older yolo started recorded no setup
// and carries none in its name; it is listed, never dropped, with `?` and the footnote in the
// table and null in JSON (§12.2). Its run_as is self: no container runs as the yolo account.
func TestPsAnUnknownSetupIsAQuestionMarkAndAFootnote(t *testing.T) {
	f := &psFake{podman: "yolo-old-thing-3f9a2c1d\tUp 5 days\t0\n"}
	deps, out, _ := psFixture(t, "linux", f, "podman")
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-old-thing-3f9a2c1d", Workspace: "/home/me/old-thing"}))
	psRun(deps)
	lines := strings.Split(out.String(), "\n")
	if f := strings.Fields(lines[1]); len(f) != 5 || f[0] != "~/old-thing" || f[1] != "?" || f[3] != "?" || f[4] != "?" {
		t.Errorf("the row = %q; want the workspace, then ? for setup, age and packs", lines[1])
	}
	if !strings.Contains(out.String(), "? — launched by an older yolo, which did not record its setup.") {
		t.Errorf("no footnote:\n%s", out.String())
	}
	j := psJSON(t, deps).Jails[0]
	if j.Setup != nil || j.Started != nil || j.Packs != nil || j.YoloVersion != nil || j.RunAs == nil || *j.RunAs != "self" {
		t.Errorf("the row in JSON = %+v; want null setup, started, packs and version, and run_as self", j)
	}
}

// TestPsIgnoresYOLOSETUP: every launch exports YOLO_SETUP into its session, so a `yolo ps` typed
// inside one would otherwise be narrowed silently (§12.1). The real deps read no selected setup,
// and the listing is the same with the variable set.
func TestPsIgnoresYOLOSETUP(t *testing.T) {
	f := &psFake{podman: "yolo-a-1\tUp\t0\n"}
	deps, out, _ := psFixture(t, "linux", f, "podman")
	t.Setenv("YOLO_SETUP", "seatbelt-self")
	psRun(deps)
	if !strings.Contains(out.String(), "WORKSPACE") || !f.asked("podman") {
		t.Errorf("YOLO_SETUP narrowed the list:\n%s", out.String())
	}
	if real := psRealDeps("", nil); real.Only != nil {
		t.Errorf("psRealDeps narrowed to %v with no --setup", real.Only)
	}
}

// TestPsSortsByWorkspaceThenSetupThenName: two setups of one project sit side by side, a
// workspace under your home prints as ~, and the order never depends on which source answered
// first (§12.3).
func TestPsSortsByWorkspaceThenSetupThenName(t *testing.T) {
	f := &psFake{}
	deps, out, base := psFixture(t, "linux", f, "podman")
	rec := func(name, ws, s string) {
		must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: name, Setup: s, Workspace: ws, Packs: []string{}}))
	}
	rec("yolo-z", "/home/me/b", "podman-self")
	rec("yolo-y", "/home/me/a", "zeta")
	rec("yolo-x", "/home/me/a", "alpha")
	rec("yolo-w", "/srv/c", "podman-self")
	f.podman = "yolo-z\tUp\t0\nyolo-y\tUp\t0\nyolo-x\tUp\t0\nyolo-w\tUp\t0\n"
	plantPsSession(t, base, "yolo-h", "live", &runtime.SessionRecord{Notch: runtime.NotchHost,
		Workspace: "/home/me/a", Name: "yolo-h", Setup: "host-self", PID: os.Getpid()})
	psRun(deps)
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n")[1:] {
		f := strings.Fields(line)
		got = append(got, f[0]+" "+f[1])
	}
	want := []string{"~/a alpha", "~/a host-self", "~/a zeta", "~/b podman-self", "/srv/c podman-self"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

// TestPsEmptyStates: each ends in a step (§12.3). Every source answered with no rows names what
// was asked and how to start one; a source that did not answer is the whole answer, with no "no
// jails" sentence; a source that is not installed is not news.
func TestPsEmptyStates(t *testing.T) {
	t.Run("complete", func(t *testing.T) {
		deps, out, _ := psFixture(t, "darwin", &psFake{ac: "[]"}, "container")
		psRun(deps)
		want := "No jails running (asked Apple Container, Seatbelt and yolo host). Start one in a project: `yolo -- <agent>`.\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})
	t.Run("only host here", func(t *testing.T) {
		deps, out, _ := psFixture(t, "linux", &psFake{})
		psRun(deps)
		want := "No jails running (asked yolo host). Start one in a project: `yolo -- <agent>`.\n"
		if out.String() != want {
			t.Errorf("output = %q, want %q", out.String(), want)
		}
	})
	t.Run("a source did not answer", func(t *testing.T) {
		deps, out, _ := psFixture(t, "linux", &psFake{podmanErr: errors.New("exit status 125")}, "podman")
		psRun(deps)
		text := out.String()
		if strings.Contains(text, "No jails running") ||
			!strings.Contains(text, "Podman is installed but not answering (exit status 125); no yolo jail is recorded on it. See why:") ||
			!strings.Contains(text, "    podman info") {
			t.Errorf("output:\n%s", text)
		}
	})
	t.Run("in a jail", func(t *testing.T) {
		deps, out, _ := psFixture(t, "linux", &psFake{})
		deps.InJail = true
		psRun(deps)
		if !strings.HasPrefix(out.String(), "Jails inside this jail:\n") || psJSON(t, deps).Frame != "jail" {
			t.Errorf("output = %q; want the frame said first", out.String())
		}
	})
}

// TestPsProblemsNameTheirNextStep: a container whose workspace is gone names the stop that ends it,
// a session's names closing its terminal, and a Podman jail stuck provisioning names `yolo doctor`
// (§12.2). Plain output carries no escape, and color stripped is the plain bytes.
func TestPsProblemsNameTheirNextStep(t *testing.T) {
	f := &psFake{podman: "yolo-gone-1\tUp\t0\nyolo-stuck-2\tUp\t0\n",
		top: map[string]string{"yolo-stuck-2": "COMMAND\nbash\nyolo-entrypo\n"}}
	deps, out, base := psFixture(t, "linux", f, "podman")
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-gone-1", Setup: "podman-self", Workspace: "/home/me/gone"}))
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-stuck-2", Setup: "podman-self", Workspace: "/home/me/here"}))
	plantPsSession(t, base, "yolo-s", "live", &runtime.SessionRecord{Notch: runtime.NotchHost,
		Workspace: "/home/me/gone2", Name: "yolo-s", Setup: "host-self", PID: os.Getpid()})
	deps.PathIsDir = func(p string) bool { return p == "/home/me/here" }
	psRun(deps)
	plain := out.String()
	for _, want := range []string{
		"\n3 problems:\n", "  ~/gone  workspace gone. Stop it:\n    podman stop yolo-gone-1\n",
		"  ~/gone2  workspace gone. It ends when its agent exits; close the terminal running it.\n",
		"  ~/here  stuck provisioning (", "Diagnose it:\n    yolo doctor\n",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("the problems lack %q:\n%s", want, plain)
		}
	}
	if runtime.StuckReasonFromTop("COMMAND\nbash\nyolo-entrypo\n") == "" {
		t.Fatal("fixture: the stuck top output is not stuck")
	}
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("plain output has ANSI: %q", plain)
	}
	var col bytes.Buffer
	deps.Out, deps.Color = &col, true
	psRun(deps)
	if !strings.Contains(col.String(), "\x1b[") || stripANSI(col.String()) != plain {
		t.Errorf("color stripped != plain:\n color=%q\n plain=%q", col.String(), plain)
	}
	for _, j := range psJSON(t, deps).Jails {
		if len(j.Problems) != 1 {
			t.Errorf("%s has problems %+v, want one", j.Name, j.Problems)
		}
	}
}

// TestPsPrunesOnEverySourcesAnswer: the prune is handed every container source's answer, so a Mac
// where both runtimes listed prunes an older yolo's dead record again (slice 11d), while one source
// that did not answer, or is not installed (a PATH miss), keeps it (SU-D10, SUP-D52).
func TestPsPrunesOnEverySourcesAnswer(t *testing.T) {
	legacy := func(t *testing.T) {
		t.Helper()
		must(t, os.MkdirAll(paths.ContainerDir(), 0o755))
		must(t, os.WriteFile(filepath.Join(paths.ContainerDir(), "yolo-dead-1"), []byte("/ws\n"), 0o644))
		must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-live-2", Workspace: "/ws2",
			Confinement: setup.ConfinementAppleContainer}))
	}
	for _, tc := range []struct {
		name   string
		f      *psFake
		bins   []string
		pruned bool
	}{
		{"both listed", &psFake{ac: `[{"id":"yolo-live-2","status":{"state":"running"}}]`}, []string{"podman", "container"}, true},
		{"apple container unreachable", &psFake{acErr: errors.New("down")}, []string{"podman", "container"}, false},
		{"apple container not installed", &psFake{}, []string{"podman"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, _, _ := psFixture(t, "darwin", tc.f, tc.bins...)
			legacy(t)
			psRun(deps)
			if _, ok := runtime.ReadLaunchRecord("yolo-dead-1"); ok == tc.pruned {
				t.Errorf("the dead legacy record kept=%v, want pruned=%v", ok, tc.pruned)
			}
			if _, ok := runtime.ReadLaunchRecord("yolo-live-2"); !ok {
				t.Error("the running Apple Container jail's record was deleted")
			}
		})
	}
	t.Run("linux, podman listed", func(t *testing.T) {
		deps, _, _ := psFixture(t, "linux", &psFake{}, "podman")
		must(t, os.MkdirAll(paths.ContainerDir(), 0o755))
		must(t, os.WriteFile(filepath.Join(paths.ContainerDir(), "yolo-dead-1"), []byte("/ws\n"), 0o644))
		psRun(deps)
		if _, ok := runtime.ReadLaunchRecord("yolo-dead-1"); ok {
			t.Error("on Linux Podman is every container source; its answer prunes the dead record")
		}
	})
}

// TestPsAnUnparseableAppleContainerListingIsUnreachable: output that is not the measured JSON is
// "could not enumerate", never "no jails", so nothing is pruned on it (D11).
func TestPsAnUnparseableAppleContainerListingIsUnreachable(t *testing.T) {
	deps, _, _ := psFixture(t, "darwin", &psFake{ac: "ID IMAGE STATE\n"}, "container")
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-x", Workspace: "/ws",
		Confinement: setup.ConfinementAppleContainer}))
	doc := psJSON(t, deps)
	if doc.Sources[1].State != "unreachable" || doc.Complete {
		t.Errorf("sources = %+v", doc.Sources)
	}
	if _, ok := runtime.ReadLaunchRecord("yolo-x"); !ok {
		t.Error("an unparseable listing pruned a record")
	}
}

// TestPsSetupNarrowsToOneSetup is slice 11e (§12.6): --setup asks that setup's technology alone,
// lists only its rows, counts the rows whose setup is not recorded, and prunes nothing.
func TestPsSetupNarrowsToOneSetup(t *testing.T) {
	f := &psFake{podman: "yolo-a-podman-self\tUp\t0\nyolo-a-lab\tUp\t0\nyolo-old\tUp\t0\n"}
	deps, out, _ := psFixture(t, "darwin", f, "podman", "container")
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-a-podman-self", Setup: "podman-self", Workspace: "/home/me/a"}))
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-a-lab", Setup: "lab", Workspace: "/home/me/a"}))
	must(t, runtime.WriteLaunchRecord(runtime.LaunchRecord{Name: "yolo-old", Workspace: "/home/me/old"}))
	must(t, os.WriteFile(filepath.Join(paths.ContainerDir(), "yolo-dead"), []byte("/ws\n"), 0o644))
	only := setup.MustBuiltIn("podman-self")
	deps.Only = &only
	psRun(deps)
	text := out.String()
	if f.asked("container") || !f.asked("podman") {
		t.Errorf("--setup podman-self asked %v; want Podman alone", f.calls)
	}
	if !strings.Contains(text, "podman-self") || strings.Contains(text, " lab ") || strings.Contains(text, "~/old") {
		t.Errorf("the narrowed list:\n%s", text)
	}
	if !strings.Contains(text, "1 jail whose setup is not recorded is not matched; `yolo ps` lists them.") {
		t.Errorf("no count of unmatched rows:\n%s", text)
	}
	if _, ok := runtime.ReadLaunchRecord("yolo-dead"); !ok {
		t.Error("a narrowed listing pruned a record; it is no answer about the other setups")
	}
	doc := psJSON(t, deps)
	if len(doc.Sources) != 1 || doc.Sources[0].Confinement != "podman" || len(doc.Jails) != 1 ||
		*doc.Sources[0].Jails != 1 {
		t.Errorf("document = %+v", doc)
	}
}

// TestPsSetupThisMachineCannotBuildListsNothingAndSaysWhy: a setup that is defined but cannot run
// here is not refused (§12.6): there is nothing to run, so it lists nothing and says why.
func TestPsSetupThisMachineCannotBuildListsNothingAndSaysWhy(t *testing.T) {
	f := &psFake{}
	deps, out, _ := psFixture(t, "darwin", f, "podman")
	only := setup.MustBuiltIn("apple-container-self")
	deps.Only = &only
	if rc := psRun(deps); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	text := out.String()
	if !strings.HasPrefix(text, "No jails running under setup `apple-container-self`.") ||
		!strings.Contains(text, "Every setup's: `yolo ps`.") || len(f.calls) != 0 {
		t.Errorf("output = %q, calls %v", text, f.calls)
	}
	if want := setup.Buildable(only, "darwin", deps.HasBinary).Error(); !strings.Contains(text, strings.TrimSpace(want)) {
		t.Errorf("output does not say why (%q):\n%s", want, text)
	}
}

// TestPsSetupFlagIsResolvedAsALaunchResolvesIt: an undefined name is refused with the launch's own
// message and exit 2 (SUP-D27), before any source is asked; a built-in resolves; --setup is taken
// out of the args the unknown-flag check reads.
func TestPsSetupFlagIsResolvedAsALaunchResolvesIt(t *testing.T) {
	withHomeAndCwd(t)
	var errw bytes.Buffer
	if _, _, rc := setupFlag("ps", []string{"ps", "--setup", "fast"}, &errw); rc != 2 ||
		!strings.Contains(errw.String(), `setup "fast" is not defined. Defined here:`) {
		t.Errorf("rc = %d, stderr %q", rc, errw.String())
	}
	rest, only, rc := setupFlag("ps", []string{"ps", "--setup=podman-self", "--format", "json"}, &errw)
	if rc != 0 || only == nil || only.Name != "podman-self" || strings.Join(rest, " ") != "ps --format json" {
		t.Errorf("rest %v, only %v, rc %d", rest, only, rc)
	}
	_, stderr := captureStdio(t, func() {
		if rc := runPs([]string{"ps", "--setup", "fast"}); rc != 2 {
			t.Errorf("runPs rc = %d, want 2", rc)
		}
	})
	if !strings.Contains(stderr, `setup "fast" is not defined`) {
		t.Errorf("runPs stderr = %q", stderr)
	}
}

// TestPsAgeIsTheLargestWholeUnit (§12.3).
func TestPsAgeIsTheLargestWholeUnit(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{4 * time.Second, "4s"}, {12*time.Minute + 59*time.Second, "12m"}, {3 * time.Hour, "3h"}, {50 * time.Hour, "2d"}} {
		if got := psAge(psNow, psNow.Add(-tc.d)); got != tc.want {
			t.Errorf("psAge(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
	if psAge(psNow, time.Time{}) != "?" {
		t.Error("an unknown start is not ?")
	}
}

// TestDetectListingRuntimeHonorsConfig covers audit finding 5: `yolo ps` (and
// prune) never consulted the config. detectListingRuntime reads the setup a launch
// selects, so the user config's `setup` naming a Seatbelt setup lists macos-user
// even on the Linux test host. RED before the commands.go wiring (detectListingRuntime
// did not exist and ps loaded no config).
func TestDetectListingRuntimeHonorsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_RUNTIME", "")
	os.Unsetenv("YOLO_RUNTIME")
	t.Setenv("YOLO_SETUP", "")
	// A Seatbelt setup rather than apple-container-self on purpose. The probe can return
	// ONLY podman or container, so pinning to container made this assertion VACUOUS
	// wherever the platform default already was container — verified on a macOS host with
	// Apple Container installed, where deleting the config lookup outright still passed.
	// macos-user is never auto-detected on any platform, so the assertion can only be
	// satisfied by the selection actually reading the config.
	if err := os.MkdirAll(home+"/.config/yolo-jail", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/.config/yolo-jail/config.jsonc", []byte(`{"setup":"seatbelt-yolo-account"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	if got := detectListingRuntime(ws); got != "macos-user" {
		t.Errorf("detectListingRuntime honoring config = %q, want macos-user", got)
	}

	// A workspace with no config and no override → the platform's NAMED default setup's
	// backend: podman-self on Linux, apple-container-self on macOS (setup.Default, chosen by
	// name and never by probing; whether it is installed is Buildable's question). What belongs
	// HERE is that a config-less workspace reaches the default instead of the previous line's
	// macos-user leaking across, which is the wiring audit finding 5 was about.
	//
	// Both platforms are stated (onPlatform) with a PATH holding only podman, so the answer
	// cannot come from the machine running the test: an expectation computed from the runtime
	// probe said "podman" on a macOS runner without Apple's container CLI on PATH, while the
	// selection rightly said "container".
	t.Setenv("HOME", t.TempDir()) // no user config, so no `setup`
	bin := t.TempDir()
	if err := os.Symlink("/bin/true", filepath.Join(bin, "podman")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	empty := t.TempDir()
	for _, tc := range []struct {
		macOS bool
		want  string
	}{{false, "podman"}, {true, "container"}} {
		onPlatform(t, tc.macOS)
		if got := detectListingRuntime(empty); got != tc.want {
			t.Errorf("macOS=%v: detectListingRuntime with no config = %q, want the named default's %q",
				tc.macOS, got, tc.want)
		}
	}
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
