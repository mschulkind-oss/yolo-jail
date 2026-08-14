package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE MISSING TEST, and it is the point of this whole change.
//
// notePackHostAccess switched on a hardcoded `KindMount, KindReadsHost, KindEnv` and DROPPED
// every other claim kind. Nothing caught it — not because the fix is hard, but because
// "which kinds does the launch disclosure cover" was a fact only the printer knew, so a kind
// added to packdecl's closed set was silently outside the transparency half of the approval
// model (loophole-packaging.md §3.3, §4.3 G4).
//
// This fails the moment a kind exists that the classification does not name. A new kind's
// author then has to make one decision — does this cross the boundary, and is the crossing a
// READ or an EXECUTION — which is exactly the decision the hardcoded set let them skip.
func TestDisclosureClassifiesEveryKnownKind(t *testing.T) {
	for _, k := range packdecl.KnownKinds() {
		if _, ok := disclosureClasses[k]; !ok {
			t.Errorf("kind %q is in packdecl's closed set but unclassified in "+
				"disclosureClasses — decide whether it crosses to the host, and whether "+
				"the crossing is a READ (printed at the banner) or an EXECUTION (printed "+
				"BEFORE startLoopholes)", k)
		}
	}
}

// Every REVIEW-WORTHY kind is disclosed, with one named exception.
//
// The review-worthy flag is packdecl's own answer to "should a human look at this before
// trusting the pack", so a review-worthy kind absent from the launch disclosure is a claim
// the user approved once and can never see again. `state` is the deliberate exclusion —
// machine-scope state leaks across workspaces but is a subtree of the JAIL's home that yolo
// owns, not a host path the pack touches — and it is named here so that reasoning has to be
// restated (or refuted) rather than quietly discovered.
func TestDisclosureCoversEveryReviewWorthyKind(t *testing.T) {
	// The exclusions, each with the reason it is not host access.
	excluded := map[packdecl.Kind]string{
		packdecl.KindState: "a subtree of the jail's own home, not a host path",
		// skills is MayBeReviewWorthy only through a WRAPPED PLUGIN (a plugin declaring
		// hooks/mcpServers runs code IN THE JAIL). That is jail-internal — it is what the
		// jail is — and `pack install` + `pack footprint` are where it is reviewed.
		packdecl.KindSkills: "a wrapped plugin runs code in the JAIL, not on the host",
	}
	for _, k := range packdecl.KnownKinds() {
		fp, ok := packdecl.FootprintOf(k)
		if !ok || !fp.MayBeReviewWorthy {
			continue
		}
		if why, isExcluded := excluded[k]; isExcluded {
			if disclosureClassOf(k) != disclosureSkip {
				t.Errorf("kind %q is documented as excluded (%s) but is classified for "+
					"disclosure; update one of the two", k, why)
			}
			continue
		}
		if disclosureClassOf(k) == disclosureSkip {
			t.Errorf("kind %q can produce a review-worthy claim but is classified "+
				"disclosureSkip — a review-worthy claim the launch never mentions is one the "+
				"user approved once and can never see again", k)
		}
	}
}

// An UNCLASSIFIED kind defaults to EXEC, which is the fail-closed direction: skip drops it
// silently (the original defect) and read prints it after the spawn (the ordering defect).
// Announcing it before anything runs is the only default that cannot be a disclosure the user
// sees too late.
func TestUnclassifiedKindDefaultsToExec(t *testing.T) {
	if got := disclosureClassOf(packdecl.Kind("some-future-kind")); got != disclosureExec {
		t.Errorf("an unclassified kind classified as %v, want disclosureExec — a kind this "+
			"build does not know must be announced before anything spawns, not after or never", got)
	}
	// And the loophole kind in particular, which lands in a concurrent change: until it is
	// classified, its claims must already print before the spawn.
	if got := disclosureClassOf(packdecl.Kind(packLoopholeKindName)); got != disclosureExec {
		t.Errorf("the %q kind classified as %v, want disclosureExec", packLoopholeKindName, got)
	}
}

// The claim kinds a fetched pack needs APPROVAL for are exactly the kinds the launch
// discloses. The disclosure is the transparency half of the approval model, so an approved
// claim class the launch does not print is a lockfile entry with no launch-time counterpart —
// which is the state §4.3 G4 calls out by name.
func TestEveryApprovableClaimClassIsDisclosed(t *testing.T) {
	// A pack declaring one contribution of every host-access-claiming shape.
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
		{Kind: packdecl.KindMount, Host: "datasets/acme", Into: "acme"},
		{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://x/i.sh"},
		{Kind: packdecl.KindBriefing, Into: "AGENTS.md", After: "host:AGENTS.md"},
	}}
	if claims := m.HostAccessClaims(); len(claims) != 4 {
		t.Fatalf("fixture produced %d approval claims, want 4: %v", len(claims), claims)
	}
	p := &packload.Pack{Name: "acme", Decl: m, MayAccessHost: true}
	lines := disclosedClaims([]*packload.Pack{p}, disclosureRead)
	joined := renderLines(lines)
	// Each approval claim's kind must appear. Matching the KIND rather than the exact string
	// is deliberate: G2a rules the approval string raw and unelided while the display detail
	// may abbreviate, so the two are explicitly not the same text.
	for _, want := range []string{"reads-host", "mount", "program", "briefing"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the launch disclosure never mentions %q:\n%s", want, joined)
		}
	}
}

// The two kinds the OLD hardcoded switch dropped, asserted as the regression they were: a
// curl-to-shell installer and a host-prepended briefing both read the user's host and were
// invisible at every launch.
func TestDisclosureCoversTheKindsTheHardcodedSwitchDropped(t *testing.T) {
	p := &packload.Pack{Name: "acme", MayAccessHost: true, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://acme.test/i.sh"},
			{Kind: packdecl.KindBriefing, Into: "AGENTS.md", After: "host:AGENTS.md"},
		},
	}}
	got := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead))
	if !strings.Contains(got, "acme.test/i.sh") {
		t.Errorf("a curl-to-shell installer is not disclosed:\n%s", got)
	}
	if !strings.Contains(got, "host:AGENTS.md") {
		t.Errorf("a host-prepended briefing is not disclosed:\n%s", got)
	}
}

// A pack whose claims are all jail-internal prints NOTHING. The disclosure must not become
// noise every launch, or the line that matters gets skipped with the rest.
func TestDisclosureSilentForJailInternalClaims(t *testing.T) {
	p := &packload.Pack{Name: "quiet", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindSkills, From: "skills", Into: ".claude/skills"},
			{Kind: packdecl.KindProgram, Bin: "acme", Via: "npm", Package: "acme"},
			{Kind: packdecl.KindState, At: ".acme", Scope: "workspace"},
		},
	}}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackHostAccess([]*packload.Pack{p})
	if errBuf.Len() != 0 {
		t.Errorf("a pack that crosses nothing produced output:\n%s", errBuf.String())
	}
}

// An UNAPPROVED fetched pack's host reads are absent from the disclosure, because they were
// REFUSED — the footprint already reflects the gate, and printing a refused claim would read
// as "this happened".
func TestDisclosureOmitsRefusedClaims(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindMount, Host: "datasets/acme", Into: "acme"},
	}}
	refused := &packload.Pack{Name: "acme", Decl: m, MayAccessHost: false}
	if got := disclosedClaims([]*packload.Pack{refused}, disclosureRead); len(got) != 0 {
		t.Errorf("a refused mount was disclosed as if it happened: %v", got)
	}
	granted := &packload.Pack{Name: "acme", Decl: m, MayAccessHost: true}
	if got := disclosedClaims([]*packload.Pack{granted}, disclosureRead); len(got) != 1 {
		t.Errorf("an APPROVED mount was not disclosed: %v", got)
	}
}

// The read disclosure goes to STDERR, like every other launch notice: a launch is usually
// `yolo -- cmd` and the user redirects the COMMAND's stdout.
func TestDisclosureWritesToStderr(t *testing.T) {
	p := &packload.Pack{Name: "acme", MayAccessHost: true, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindReadsHost, Host: ".netrc"}},
	}}
	var outBuf, errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = &outBuf, &errBuf
	o.notePackHostAccess([]*packload.Pack{p})
	if outBuf.Len() != 0 {
		t.Errorf("the disclosure leaked to stdout:\n%s", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), ".netrc") {
		t.Errorf("the disclosure did not reach stderr:\n%s", errBuf.String())
	}
}

// --- THE ORDERING (§4.3 G4) ---

// THE ORDERING INVARIANT: the host-EXECUTION disclosure PRECEDES the spawn.
//
// This is the substantive half of item 6. startLoopholes ran at one point in the pipeline and
// notePackHostAccess an entire phase LATER, so the spawn preceded the notice — and the spawn
// is silent on success. A fetched pack's daemon could start on every launch for months with
// the only host-side record being a lockfile the user has to go read.
//
// Asserted against the SPAWN SIDE'S OWN FIRST SIDE EFFECT, not against a log the test writes
// on both sides. startLoopholes' first statement creates the per-jail host-services dir —
// before its Apple Container early return, before any daemon — so "does that dir exist yet?"
// observed from inside the disclosure is a direct reading of the ordering. Asserting merely
// that the line got printed would PASS under the old ordering, which is exactly how the
// defect survived a year.
func TestHostExecDisclosurePrecedesTheSpawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	cname := "yolo-ordering-" + t.Name()
	socketsDir := hostServiceSocketsDir(cname, false)
	if _, err := os.Lstat(socketsDir); err == nil {
		t.Fatalf("fixture is not clean: %s already exists", socketsDir)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })

	dirExistedAtDisclosure := true // pessimistic: the assertion must have to be earned
	disclosed := false
	stubHostExecClaims(t, func([]*packload.Pack) []disclosureLine {
		disclosed = true
		_, err := os.Lstat(socketsDir)
		dirExistedAtDisclosure = err == nil
		return []disclosureLine{{"acme", "loophole acme-proxy RUNS `python3 acme-daemon.py`"}}
	})

	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false } // no cgroup delegate
	// rt "podman" so startLoopholes runs its real body (Apple Container returns early, which
	// would still create the dir but is the inert path this test is not about).
	o.startLoopholesDisclosed(cname, "podman", newConfig(), nil)

	if !disclosed {
		t.Fatal("startLoopholesDisclosed never ran the host-exec disclosure")
	}
	if dirExistedAtDisclosure {
		t.Error("the host-services dir already existed when the exec disclosure ran, so " +
			"startLoopholes had already begun — the disclosure must PRECEDE the spawn " +
			"(§4.3 G4: after the spawn it is a notification that something already happened)")
	}
	if _, err := os.Lstat(socketsDir); err != nil {
		t.Errorf("startLoopholes never ran after the disclosure (%v); the test would pass "+
			"vacuously", err)
	}
	if !strings.Contains(errBuf.String(), "runs pack code on your machine") {
		t.Errorf("the exec disclosure did not print its heading:\n%s", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "acme-daemon.py") {
		t.Errorf("the exec disclosure did not name the argv:\n%s", errBuf.String())
	}
}

// THE ONE CALL SITE. The ordering is a property of startLoopholesDisclosed, so it survives a
// refactor only while nothing else calls startLoopholes — otherwise a second path spawns with
// no disclosure and the invariant silently reverts to the shape it had.
func TestStartLoopholesHasOneDisclosedCallSite(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, f := range files {
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, rerr := os.ReadFile(name)
		if rerr != nil {
			t.Fatal(rerr)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "o.startLoopholes(") {
				continue
			}
			// The wrapper's own call is the sanctioned one.
			if name == "packloopholes.go" {
				continue
			}
			offenders = append(offenders, name+":"+itoaTest(i+1)+": "+strings.TrimSpace(line))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("startLoopholes is called outside startLoopholesDisclosed, so that path "+
			"spawns host code with no disclosure before it (§4.3 G4):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// The exec disclosure is SILENT when nothing runs on the host — the ordinary case for every
// pack shipped today, so it must not add a line to every launch.
func TestHostExecDisclosureSilentWithNoExecClaims(t *testing.T) {
	p := &packload.Pack{Name: "acme", MayAccessHost: true, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindReadsHost, Host: ".netrc"}},
	}}
	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackHostExec([]*packload.Pack{p})
	if errBuf.Len() != 0 {
		t.Errorf("a host READ produced an EXEC disclosure:\n%s", errBuf.String())
	}
}

// --- helpers ---

// stubHostExecClaims replaces the exec-claim producer for one test. The seam exists because
// the only kind that produces an exec claim (`loophole`) lands in a concurrent change, and
// without it the ORDERING invariant could not be pinned until after the kind existed — one
// batch too late, which is how the defect survived.
func stubHostExecClaims(t *testing.T, fn func([]*packload.Pack) []disclosureLine) {
	t.Helper()
	orig := packHostExecClaims
	packHostExecClaims = fn
	t.Cleanup(func() { packHostExecClaims = orig })
}

func renderLines(lines []disclosureLine) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.pack + ": " + l.claim + "\n")
	}
	return b.String()
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
