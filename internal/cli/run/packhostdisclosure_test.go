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

// EVERY host-crossing claim class is disclosed at the launch.
//
// It was TestEveryApprovableClaimClassIsDisclosed, keyed off packdecl.Manifest.HostAccessClaims
// — the set `pack install` prompted on — so an approved class with no launch-time counterpart
// (§4.3 G4) failed here. OQ-TP9 deleted the prompt and that helper, and the property got MORE
// load-bearing rather than less: the banner is now the only place a user learns any of this,
// so the four classes are enumerated directly and the count is an equality.
func TestEveryHostCrossingClaimClassIsDisclosed(t *testing.T) {
	// A pack declaring one contribution of every host-crossing shape pack.json can express.
	// (The two crossings declared OUTSIDE pack.json — a wrapped plugin's code-running
	// components and a shipped loophole's daemon/binds/devices — are covered where their
	// fixtures live: packnohostgate_test.go and loopholenoorigingate_test.go.)
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindReadsHost, Host: ".claude/settings.json"},
		{Kind: packdecl.KindMount, Host: "datasets/acme", Into: "acme"},
		{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://x/i.sh"},
		{Kind: packdecl.KindBriefing, Into: "AGENTS.md", After: "host:AGENTS.md"},
	}}
	p := &packload.Pack{Name: "acme", Decl: m}
	lines := disclosedClaims([]*packload.Pack{p}, disclosureRead)
	joined := renderLines(lines)
	if len(lines) != 4 {
		t.Fatalf("the launch disclosed %d of the fixture's 4 crossings:\n%s", len(lines), joined)
	}
	// Matching the KIND rather than an exact string: the display detail may abbreviate, and
	// pinning its wording here would make every copy-edit a test failure.
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
	p := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "acme", Via: "installer", URL: "https://acme.test/i.sh"},
			{Kind: packdecl.KindBriefing, Into: "AGENTS.md", After: "host:AGENTS.md"},
		},
	}}
	got := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead))
	if !strings.Contains(got, "acme.test/i.sh") {
		t.Errorf("a curl-to-shell installer is not disclosed:\n%s", got)
	}
	// "~/AGENTS.md", not "host:AGENTS.md": since 2026-09-04 the banner renders a claim as a
	// SENTENCE (packload.Claim.DisclosureSentence), and one of the three things
	// pack-execution-trust.md §6 required was that a host path show the root it is relative
	// to instead of the `host:` prefix a manifest spells it with.
	if !strings.Contains(got, "~/AGENTS.md") {
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

// EVERY pack's host reads are disclosed, whoever shipped it.
//
// It was TestDisclosureOmitsRefusedClaims, and its subject was a real asymmetry: an
// unapproved fetched pack's mount was about to be REFUSED, so printing it would have read as
// "this happened". OQ-TP9 deleted the refusal, and the same reasoning now runs the other way
// — every declared mount happens, so omitting one would HIDE a crossing rather than avoid
// announcing a non-event. run.claimWillHappen, the predicate that did the subtracting, is
// deleted with it.
//
// Asserted on a Pack constructed by hand, carrying no provenance at all, because that is what
// pins the property: disclosedClaims consults NOTHING about where the pack came from, so an
// `if <origin>` reintroduced in claimWillHappen's old position turns this red.
func TestDisclosureIncludesEveryPacksHostReads(t *testing.T) {
	m := &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindMount, Host: "datasets/acme", Into: "acme"},
	}}
	p := &packload.Pack{Name: "acme", Decl: m}
	if got := disclosedClaims([]*packload.Pack{p}, disclosureRead); len(got) != 1 {
		t.Errorf("a declared mount was not disclosed: %v", got)
	}
}

// The read disclosure goes to STDERR, like every other launch notice: a launch is usually
// `yolo -- cmd` and the user redirects the COMMAND's stdout.
func TestDisclosureWritesToStderr(t *testing.T) {
	p := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
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
	p := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
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

// --- convergence with the `loophole` kind's per-claim RunsHostCode ---

// THE READ/EXEC SPLIT IS PER CLAIM, NOT PER KIND. One loophole contribution emits several
// claims, and only some of them execute: the daemon argv runs code on the user's machine,
// while an intercept's CA, a `:ro` bind and a passed-through device cross the boundary
// without running anything.
//
// Both directions matter and they fail differently. Classifying every loophole claim as EXEC
// would put a CA and a device in the block whose whole value is that it is short and every
// line in it is about to run — crying wolf. Classifying them all as READ would print the
// daemon argv AFTER the spawn, which is the ordering defect §4.3 G4 exists to fix.
func TestLoopholeClaimsSplitByRunsHostCodeNotByKind(t *testing.T) {
	p := writeRealLoopholePack(t, "acme", "acme-proxy", `{
		"name": "acme-proxy",
		"transport": "none",
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py", "--socket", "{socket}"]},
		"intercepts": [{"host": "api.acme.test"}],
		"host_devices": ["/dev/snd"]
	}`)

	execLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureExec))
	readLines := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureRead))

	// The daemon argv is EXEC, and it is the only exec claim here.
	if !strings.Contains(execLines, "acme-daemon.py") {
		t.Errorf("the daemon argv is not in the pre-spawn block:\n%s", execLines)
	}
	for _, mustNotExec := range []string{"api.acme.test", "/dev/snd"} {
		if strings.Contains(execLines, mustNotExec) {
			t.Errorf("%q is in the pre-spawn EXEC block but runs no host code — that block's "+
				"value is that every line in it is about to run:\n%s", mustNotExec, execLines)
		}
	}
	// The non-executing crossings are still DISCLOSED, in the read block.
	for _, mustRead := range []string{"api.acme.test", "/dev/snd"} {
		if !strings.Contains(readLines, mustRead) {
			t.Errorf("%q crosses the boundary but is disclosed nowhere:\n%s", mustRead, readLines)
		}
	}
	if strings.Contains(readLines, "acme-daemon.py") {
		t.Errorf("the daemon argv is in the post-spawn READ block — after the spawn it is a "+
			"notification that something already happened (§4.3 G4):\n%s", readLines)
	}
}

// An UNREADABLE loophole declaration is disclosed as EXEC. The claim producer fails closed
// there ("a manifest yolo cannot read may well declare a host daemon") and the disclosure has
// to agree, or the one case where yolo cannot see what is about to run would be the one case
// it announces too late.
func TestUnreadableLoopholeDeclarationIsDisclosedAsExec(t *testing.T) {
	p := writeRealLoopholePack(t, "acme", "acme-proxy", `{not json`)
	got := renderLines(disclosedClaims([]*packload.Pack{p}, disclosureExec))
	if !strings.Contains(got, "acme-proxy") {
		t.Errorf("an unreadable loophole declaration is not disclosed before the spawn:\n%s", got)
	}
}

// The real END-TO-END shape of §4.3 G4: a pack whose staged tree declares a real loophole
// prints its daemon argv through the production path — no test seam — and prints it before the
// spawn.
func TestRealLoopholePackDisclosesItsDaemonBeforeTheSpawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	p := writeRealLoopholePack(t, "acme", "acme-proxy", `{
		"name": "acme-proxy",
		"transport": "none",
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/acme-daemon.py", "--socket", "{socket}"]}
	}`)

	cname := "yolo-e2e-disclose-" + t.Name()
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })
	dirExistedAtPrint := true
	seen := ""

	var errBuf lineWatcher
	errBuf.onWrite = func(s string) {
		if strings.Contains(s, "acme-daemon.py") {
			seen = s
			_, err := os.Lstat(socketsDir)
			dirExistedAtPrint = err == nil
		}
	}
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false }
	o.startLoopholesDisclosed(cname, "podman", newConfig(), []*packload.Pack{p})

	if seen == "" {
		t.Fatalf("the real production path never disclosed the daemon argv; wrote:\n%s", errBuf.all)
	}
	if dirExistedAtPrint {
		t.Error("the daemon argv printed AFTER startLoopholes had begun — for an exec claim " +
			"that is a notification, not a disclosure (§4.3 G4)")
	}
}

// lineWatcher is an io.Writer that lets a test observe the WORLD at the moment a particular
// line is written. That is what makes the ordering assertion above a reading of the real
// pipeline rather than of a stub: the check runs inside the print, so nothing can reorder
// between them.
type lineWatcher struct {
	all     string
	onWrite func(string)
}

func (w *lineWatcher) Write(b []byte) (int, error) {
	s := string(b)
	w.all += s
	if w.onWrite != nil {
		w.onWrite(s)
	}
	return len(b), nil
}

// writeRealLoopholePack writes a pack whose pack.json declares the REAL `loophole` kind and
// loads it through packload, so the claims come from the production producer rather than a
// hand-built Manifest. This is the fixture that became possible once the kind landed.
func writeRealLoopholePack(t *testing.T, packName, loopholeName, manifestBody string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "loopholes", loopholeName)
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifestBody), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"contributes":[{"kind":"loophole","from":"loopholes/` + loopholeName + `"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, packName)
	if len(probs) > 0 {
		t.Fatalf("the loophole pack fixture does not load: %v", probs)
	}
	return p
}
