package run

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

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
	o.notePackLoopholesInert(rt, packs)
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
				`{"name": "acme-proxy", "transport": "none"}`)
			got := inertOutput(t, rt, p)
			if !strings.Contains(got, loopholeInertLineMarker) {
				t.Fatalf("backend %s printed no inert line:\n%s", rt, got)
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
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "transport": "none"}`)
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
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "transport": "none"}`)
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
		`{"name": "acme-proxy", "transport": "none", "platforms": ["`+other+`"]}`)
	// A WORKING backend, so the only reason left is the platform — which is what proves the
	// two axes share one mechanism rather than the backend answer covering for a missing one.
	got := inertOutput(t, "podman", p)
	if !strings.Contains(got, loopholeInertLineMarker) {
		t.Fatalf("a platform-unsupported loophole printed no inert line:\n%s", got)
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
		`{"name": "acme-proxy", "transport": "none", "platforms": ["`+runtime.GOOS+`"]}`)
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
		`{"name": "acme-proxy", "transport": "none", "platforms": ["`+other+`"]}`)
	got := inertOutput(t, "container", p)
	if strings.Count(got, loopholeInertLineMarker) != 1 {
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
	p := writeLoopholePack(t, "acme", "acme-proxy", `{"name": "acme-proxy", "transport": "none"}`)
	var outBuf, errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = &outBuf, &errBuf
	o.notePackLoopholesInert("container", []*packload.Pack{p})
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
