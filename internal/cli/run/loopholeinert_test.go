package run

import (
	"bytes"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// inertOutput runs the inert report for one backend over the given packs and returns stderr.
func inertOutput(t *testing.T, rt string, packs ...*packload.Pack) string {
	t.Helper()
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackLoopholesInert(rt, packs, nil)
	return errBuf.String()
}

// BOTH INERT BACKENDS PRINT (§8 item 2). This is the B-0 rule applied to the new kind: run.go
// records B-0 as "a backend that looked provisioned and configured nothing", and the pipeline
// was restructured to end it. A pack whose whole purpose is a loophole must not look
// installed on a backend that ignores it.
//
// Draft 1 of the design scoped this to one narrow slice of one backend (the `intercepts`
// container-args skip). Measured, both skips are much wider: Apple Container returns from
// startLoopholes before ANY external service starts, so every pack-shipped host daemon is
// skipped there whether it intercepts or not; and macos-user returns from Run() long before
// startLoopholes is reached at all, so the kind is inert on that backend entirely.
func TestBothInertBackendsReportByName(t *testing.T) {
	for _, rt := range []string{"container", "macos-user"} {
		t.Run(rt, func(t *testing.T) {
			p := writeLoopholePack(t, "acme", "acme-proxy",
				`{"name": "acme-proxy", "default_enabled": true, "transport": "none"}`)
			got := inertOutput(t, rt, p)
			// The EXACT line the report renders, built from the same inputs — so this cannot
			// pass on a partial match, and cannot rot into matching nothing when either axis's
			// wording changes (see inertLineFor).
			want := inertLineFor("acme", loopholes.InertNote{
				Name: "acme-proxy", Axis: loopholes.AxisBackend, Reason: backendInertReason(rt),
			})
			if !strings.Contains(got, want) {
				t.Fatalf("backend %s did not print the inert line\n want: %s\n  got: %s", rt, want, got)
			}
			// BY NAME, both halves: which pack, and which loophole. A line naming neither is
			// unactionable on a machine with several packs.
			for _, want := range []string{"acme", "acme-proxy"} {
				if !strings.Contains(got, want) {
					t.Errorf("the inert line does not name %q:\n%s", want, got)
				}
			}
			// ONE line, not a paragraph: this prints on every launch on that backend.
			if lines := strings.Count(strings.TrimRight(got, "\n"), "\n") + 1; lines != 1 {
				t.Errorf("the inert report is %d lines for one loophole:\n%s", lines, got)
			}
		})
	}
}

// The report names the REASON, not just the fact — "and here is why" is half of the one
// answer shape §3.1 asks the two axes to share.
func TestInertBackendLineExplainsWhy(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "default_enabled": true, "transport": "none"}`)
	if got := inertOutput(t, "container", p); !strings.Contains(got, "Apple Container") {
		t.Errorf("the container line does not name the backend:\n%s", got)
	}
	if got := inertOutput(t, "macos-user", p); !strings.Contains(got, "macos-user") {
		t.Errorf("the macos-user line does not name the backend:\n%s", got)
	}
}

// A WORKING backend prints nothing. The report must not become a line on every podman launch,
// or the one time it matters gets skipped with the rest.
func TestWorkingBackendPrintsNoInertLine(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "default_enabled": true, "transport": "none"}`)
	if got := inertOutput(t, "podman", p); got != "" {
		t.Errorf("podman produced an inert line:\n%s", got)
	}
}

// A pack shipping NO loophole prints nothing, even on an inert backend. Every pack shipped
// today is in this state, so the alternative would be a spurious line on every AC launch.
//
// The fixture carries an EMPTY manifest rather than a nil one, matching packload's own
// invariant (Pack.Decl is never nil — a pack with no pack.json gets an empty one, because a
// skills-only pack must stay zero-ceremony). A nil-Decl fixture would test a state the loader
// cannot produce.
func TestInertReportSilentWithoutALoopholeContribution(t *testing.T) {
	p := &packload.Pack{Name: "plain", Root: t.TempDir(), Decl: &packdecl.Manifest{}}
	if got := inertOutput(t, "container", p); got != "" {
		t.Errorf("a pack with no loophole produced an inert line:\n%s", got)
	}
}

// ONE MECHANISM, TWO AXES (§3.1, §8): the PLATFORM declaration feeds the same report. Two
// half-messages for one user-visible situation ("this loophole does nothing here") is how B-0
// happened in the first place.
func TestPlatformUnsupportedUsesTheSameReport(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "default_enabled": true, "transport": "none", "platforms": ["`+other+`"]}`)
	// A WORKING backend, so the only reason left is the platform — which is what proves the
	// two axes share one mechanism rather than the backend answer covering for a missing one.
	got := inertOutput(t, "podman", p)
	if !strings.Contains(got, "loophole acme-proxy is ") {
		t.Fatalf("a platform-unsupported loophole printed no inert line:\n%s", got)
	}
	// The AXIS is carried as data, so a reader with two lines can tell "wrong machine" from
	// "wrong backend" — and this asserts THE PRODUCER'S OWN NOTE, taken from
	// loopholes.PlatformInertNotes over the same resolved record, rather than a lookalike
	// sentence assembled here. Asking the producer is what makes "one mechanism" checkable: if
	// the report ever re-derives its own reason again, this stops matching.
	notes := loopholes.PlatformInertNotes([]*loopholes.Loophole{
		resolveInertLoophole(packLoopholes(p)[0].Dir),
	})
	if len(notes) != 1 {
		t.Fatalf("the producer yielded %d notes for one unsupported loophole", len(notes))
	}
	if want := inertLineFor("acme", notes[0]); !strings.Contains(got, want) {
		t.Errorf("the platform line is not the producer's own note\n want: %s\n  got: %s", want, got)
	}
	if !strings.Contains(got, "acme-proxy") {
		t.Errorf("the platform line does not name the loophole:\n%s", got)
	}
	// The schema's own wording, not a second copy: PlatformsUnsupportedReason exists precisely
	// so a report and a gate cannot disagree about one declaration.
	for _, want := range []string{"unsupported on " + runtime.GOOS, "Nothing is missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("the platform line does not carry the schema's reason %q:\n%s", want, got)
		}
	}
}

// A loophole SUPPORTED here on a working backend prints nothing — the control for the test
// above, which would otherwise pass on any pack at all.
func TestSupportedPlatformPrintsNothing(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "default_enabled": true, "transport": "none", "platforms": ["`+runtime.GOOS+`"]}`)
	if got := inertOutput(t, "podman", p); got != "" {
		t.Errorf("a supported loophole on a working backend produced a line:\n%s", got)
	}
}

// BACKEND BEATS PLATFORM when both apply: an inert backend starts no host service whatever
// the platform says, so the platform answer would be a second reason for one outcome — and
// the line a user can act on is "switch backends", not "get a different machine".
func TestBackendReasonWinsOverPlatformReason(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "default_enabled": true, "transport": "none", "platforms": ["`+other+`"]}`)
	got := inertOutput(t, "container", p)
	if strings.Count(got, "loophole acme-proxy is ") != 1 {
		t.Errorf("want exactly one inert line when both axes apply:\n%s", got)
	}
	if !strings.Contains(got, "Apple Container") {
		t.Errorf("the backend reason did not win:\n%s", got)
	}
}

// An UNREADABLE manifest prints nothing here. The discovery layer's contract is
// warn-and-continue and it already warns about the same file; a second complaint from the
// launch path would read as a second, unrelated bug.
func TestUnreadableLoopholeManifestIsSilentInTheInertReport(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy", `{not json`)
	if got := inertOutput(t, "podman", p); got != "" {
		t.Errorf("an unreadable manifest produced an inert line:\n%s", got)
	}
}

// The report goes to STDERR, like every other launch notice.
func TestInertReportWritesToStderr(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "default_enabled": true, "transport": "none"}`)
	var outBuf, errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = &outBuf, &errBuf
	o.notePackLoopholesInert("container", []*packload.Pack{p}, nil)
	if outBuf.Len() != 0 {
		t.Errorf("the inert report leaked to stdout:\n%s", outBuf.String())
	}
	if errBuf.Len() == 0 {
		t.Error("the inert report did not reach stderr")
	}
}

// backendInertReason must answer for EVERY shipped backend, and only the two that are
// actually inert may answer non-empty. A new backend added without a decision here would
// silently inherit "not inert" — which is the right default, but the list is asserted so the
// decision is visible.
func TestBackendInertReasonCoversEveryShippedBackend(t *testing.T) {
	want := map[string]bool{"podman": false, "container": true, "macos-user": true}
	for rt, inert := range want {
		if got := backendInertReason(rt) != ""; got != inert {
			t.Errorf("backendInertReason(%q) inert=%v, want %v", rt, got, inert)
		}
	}
}

// --- ONE MECHANISM MEANS ONE SELECTION, NOT JUST ONE RENDERING ---
//
// §3.1/§8 require one mechanism for the two axes. The RENDERING converged on
// InertNote.Line(); the SELECTION did not — this report walked pack loopholes itself and
// called a private platform reader, while loopholes.PlatformInertNotes (whose doc comment
// states the dedup and the disabled-skip as REQUIREMENTS) had zero production callers.
//
// The two tests below are the measured divergences. Both are about the platform axis, which
// is the axis the producer owns.

// DEDUP: PlatformInertNotes is "ONCE PER LOOPHOLE, by name … a duplicated line reads as two
// problems". The launch report emitted TWO identical lines for one loophole declared twice —
// which is not hypothetical: discovery merges four sources, and a pack declaring the same
// module dir under two contributions is a config mistake whose report should be one line
// about one loophole, not two about two.
func TestOneLoopholeNamedTwiceProducesOneInertLine(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "default_enabled": true, "transport": "none", "platforms": ["`+other+`"]}`)
	// The SAME module declared twice. The pack layer refuses this at staging by name; the
	// report must not turn one mistake into two problems if it ever reaches here.
	p.Decl.Contributes = append(p.Decl.Contributes, p.Decl.Contributes[0])

	got := inertOutput(t, "podman", p)
	if n := strings.Count(got, "loophole acme-proxy is "); n != 1 {
		t.Errorf("the platform axis emitted %d lines for ONE loophole; PlatformInertNotes "+
			"dedups by name precisely because 'a duplicated line reads as two problems', and "+
			"routing the report through it is what makes that the same code rather than the "+
			"same shape:\n%s", n, got)
	}
}

// DISABLED IS SKIPPED: PlatformInertNotes deliberately emits nothing for a loophole the USER
// turned off — "it is inert for a reason the user chose, already reported as 'disabled', and
// telling them a switched-off loophole also has the wrong platform is noise". The launch
// report printed the platform line anyway.
func TestADisabledLoopholeDrawsNoPlatformInertLine(t *testing.T) {
	other := "darwin"
	if runtime.GOOS == "darwin" {
		other = "linux"
	}
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "transport": "none", "default_enabled": false, "platforms": ["`+other+`"]}`)
	if got := inertOutput(t, "podman", p); got != "" {
		t.Errorf("a DISABLED loophole drew a platform inert line. The producer skips it "+
			"deliberately — the user chose this, hears 'disabled' already, and a second line "+
			"saying it also has the wrong platform is noise:\n%s", got)
	}
}

// The PRODUCER has a production caller — asserted over the SOURCE, because "a value with the
// right shape and no callers" is the exact state the divergence above lived in for a batch,
// and no runtime assertion can observe it. The producer's doc comment states the dedup and
// the disabled-skip as REQUIREMENTS; a report that stops asking it silently re-acquires both
// defects while every behavioural test still passes on whatever it does itself.
func TestThePlatformInertProducerHasAProductionCaller(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if strings.Contains(string(body), "loopholes.PlatformInertNotes(") {
			callers = append(callers, name)
		}
	}
	if len(callers) == 0 {
		t.Error("nothing in the run package calls loopholes.PlatformInertNotes. That is the " +
			"state §3.1's 'one mechanism' requirement was in for a batch: the producer existed, " +
			"documented its dedup and its disabled-skip as requirements, and had ZERO callers " +
			"while this report re-derived the platform answer itself — and the two disagreed " +
			"about a duplicate and about a disabled loophole. Route the report through it.")
	}
}

// The BACKEND axis is unaffected by the disabled-skip, and that asymmetry is deliberate
// rather than an oversight: an inert backend is a statement about the LAUNCH ("nothing this
// pack declares runs here"), not about one loophole's own switch, and it is the line a user
// can act on. Without this the fix above could quietly silence the backend report too.
func TestADisabledLoopholeStillDrawsTheBackendInertLine(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy",
		`{"name": "acme-proxy", "transport": "none", "default_enabled": false}`)
	got := inertOutput(t, "container", p)
	if !strings.Contains(got, "loophole acme-proxy is ") {
		t.Errorf("the backend axis went silent for a disabled loophole. The backend answer is "+
			"about the launch, not about the switch — a pack whose whole purpose is a loophole "+
			"must not look installed on a backend that ignores it (B-0):\n%s", got)
	}
}

// packLoopholes reads the contribution's `from` and derives the loophole name from its
// BASENAME, which §3.1 makes exact: "the loophole's `name` must equal the directory
// basename … it is what lets the footprint name the loophole without decoding its manifest."
func TestPackLoopholesNamesFromTheDirBasename(t *testing.T) {
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy"}`)
	got := packLoopholes(p)
	if len(got) != 1 {
		t.Fatalf("packLoopholes = %v, want one", got)
	}
	if got[0].Name != "acme-proxy" || got[0].Pack != "acme" {
		t.Errorf("packLoopholes = %+v", got[0])
	}
	if !strings.HasSuffix(got[0].Dir, "loopholes/acme-proxy") {
		t.Errorf("module dir %q is not the staged `from` path", got[0].Dir)
	}
}

// THE CALL SITE, not the callee. TestBothInertBackendsReportByName above calls
// notePackLoopholesInert DIRECTLY for both backends, which is the shape AGENTS.md warns
// about: it passed for macos-user while no launch on that backend could produce the line,
// because the report hangs off startLoopholesDisclosed inside runContainer and the
// macos-user arm returns above it. The callee was pinned; the call site did not exist.
//
// This drives a REAL launch — Run() with runtime=macos-user, the real claude pack (which
// ships the claude-oauth-broker loophole), a stub backend handler — and asserts the line
// reaches the user. Delete the notePackLoopholesInert call from the macos-user arm and
// this fails; the test above does not.
func TestMacosUserLaunchReportsInertLoopholes(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ bool) int {
		reached = true
		return 0
	}

	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatal("Run() never reached the macos-user handler")
	}

	got := stdout.String() + stderr.String()
	// By NAME, both halves — which pack and which loophole — for the same reason the
	// direct-call test asserts it: a line naming neither is unactionable.
	for _, want := range []string{"claude", "claude-oauth-broker", "macos-user"} {
		if !strings.Contains(got, want) {
			t.Errorf("a macos-user launch did not tell the user %q is inert (missing %q).\n"+
				"Every pack-shipped loophole is inert on this backend — it never reaches\n"+
				"startLoopholes — so a pack whose whole purpose is a loophole looks installed\n"+
				"and does nothing.\noutput:\n%s", "claude-oauth-broker", want, got)
		}
	}
}
