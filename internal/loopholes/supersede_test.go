package loopholes

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// bedrock is the design's own example claim (§2), used everywhere below so the
// tests read as the scenario the mechanism exists for.
func bedrock() PackSupersession {
	return PackSupersession{
		Pack:       "claude-bedrock",
		Capability: "claude-oauth-refresh",
		Because:    "Bedrock overrides the OAuth path; no token is ever refreshed",
	}
}

// servingLoophole builds a discoverable loophole declaring `serves`.
func servingLoophole(t *testing.T, root, name string, serves []string) {
	t.Helper()
	dir := mkdir(t, filepath.Join(root, name))
	m := map[string]any{"name": name, "description": name, "transport": "none"}
	if serves != nil {
		m["serves"] = serves
	}
	writeManifest(t, dir, m)
}

func discoverWith(root string, claims []PackSupersession) Set {
	return NewSet(DiscoverOptions{PackModules: moduleDirsUnder(root), PackSupersessions: claims})
}

// onlyModules makes a tree the caller controls THE process's whole pack-module record,
// and returns it.
//
// It does BOTH halves of what these command tests need. They go through NewHostSet, which
// reads the recorded modules — and `yolo loopholes status` EXECUTES each record's
// doctor_cmd, so against this machine's real packs the broker's `yolo internal daemon
// claude-oauth-broker --self-check` would really run, twice, under a 10s timeout. A unit
// test must not spawn yolo's own daemons.
//
// It replaces an `onlyBundled` that pointed BundledLoopholesDir at the tree; with the
// bundled channel retired (docs/design/broker-as-a-pack.md OQ-BP4), a pack module dir is
// the only source left that reads a manifest a test can write. The manifests written here
// stay inside the pack-shipped subset, which is what makes the substitution honest rather
// than merely compiling.
func onlyModules(t *testing.T) string {
	t.Helper()
	root := modsDir(t)
	// Registered as the LAZY RESOLVER rather than a staged record, because the caller
	// writes its module dirs AFTER this returns: a record taken here would be empty, and
	// the test would pass over a set with nothing in it.
	SetPackModuleResolver(func() []PackModule { return moduleDirsUnder(root) })
	ResetPackModules()
	t.Cleanup(func() {
		SetPackModuleResolver(nil)
		ResetPackModules()
	})
	return root
}

// ---------------------------------------------------------------------------
// R3 — the gate, and the invariant that silence never means "supersede me"
// ---------------------------------------------------------------------------

// TestServesSilenceIsNeverASupersession is the acceptance bar (design §8.1): a
// loophole with NO `serves` behaves exactly as it did, even when a pack claims a
// capability by every name in sight. This is what makes the mechanism additive for
// every manifest ever written, first- or third-party.
//
// It claims THREE capabilities, including the loophole's own NAME, because the
// tempting wrong implementation is to fall back to matching the name when `serves`
// is absent — which is precisely the coupling design §3 exists to refuse.
func TestServesSilenceIsNeverASupersession(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "quiet", nil)

	claims := []PackSupersession{
		{Pack: "p", Capability: "quiet", Because: "b"},
		{Pack: "p", Capability: "", Because: "b"},
		bedrock(),
	}
	set := discoverWith(root, claims)
	lp, ok := set.Lookup("quiet")
	if !ok {
		t.Fatal("loophole not discovered")
	}
	if lp.Superseded() {
		t.Fatalf("a loophole declaring no `serves` was superseded by %v — silence must "+
			"never read as a claim", lp.SupersededBy)
	}
	if !lp.Active() {
		reason, _ := lp.InactiveReason()
		t.Errorf("Active() = false (%s); a manifest with no `serves` must be unchanged", reason)
	}
}

// TestSupersessionDeactivates: serves + a matching claim ⇒ inactive, and the reason
// names the pack, the capability and the pack author's own `because` (design §8.3).
func TestSupersessionDeactivates(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})

	lp, ok := discoverWith(root, []PackSupersession{bedrock()}).Lookup("broker-like")
	if !ok {
		t.Fatal("loophole not discovered")
	}
	if !lp.Superseded() {
		t.Fatal("a served capability claimed by a selected pack did not supersede")
	}
	if lp.Active() {
		t.Error("Active() = true for a superseded loophole")
	}
	reason, ok := lp.InactiveReason()
	if !ok {
		t.Fatal("InactiveReason() reported nothing for a superseded loophole")
	}
	for _, want := range []string{"superseded", "claude-bedrock", "claude-oauth-refresh",
		"no token is ever refreshed"} {
		if !strings.Contains(reason, want) {
			t.Errorf("InactiveReason() = %q, missing %q — an unexplained disappearance is "+
				"the failure mode this design exists to avoid", reason, want)
		}
	}
}

// TestSupersessionNeedsEveryCapability is the `every`, not `any`, rule (design §4): a
// loophole serving two jobs with one superseded still has a job.
func TestSupersessionNeedsEveryCapability(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "two-jobs", []string{"job-a", "job-b"})

	one := []PackSupersession{{Pack: "p", Capability: "job-a", Because: "a is moot"}}
	lp, _ := discoverWith(root, one).Lookup("two-jobs")
	if lp.Superseded() {
		t.Fatalf("one of two served capabilities was enough: %v", lp.SupersededBy)
	}
	if !lp.Active() {
		t.Error("Active() = false while a served job remains")
	}

	both := append(one, PackSupersession{Pack: "q", Capability: "job-b", Because: "b is moot"})
	lp2, _ := discoverWith(root, both).Lookup("two-jobs")
	if !lp2.Superseded() {
		t.Fatal("both served capabilities claimed and the loophole is still live")
	}
	// BOTH claims are reported, not just the first: they come from different packs
	// with different reasons, and naming one would credit a pack that on its own
	// turned nothing off.
	reason, _ := lp2.InactiveReason()
	for _, want := range []string{"'p'", "'q'", "a is moot", "b is moot"} {
		if !strings.Contains(reason, want) {
			t.Errorf("InactiveReason() = %q, missing %q", reason, want)
		}
	}
}

// TestThreeGatesAreIndependent is design §8.7: `Enabled`, `requires` and supersession
// each deactivate on their own and none of them is a synonym for another.
func TestThreeGatesAreIndependent(t *testing.T) {
	unsetJail(t)
	base := func() *Loophole {
		return &Loophole{Name: "acme", Enabled: true, Serves: []string{"job-a"}}
	}
	if !base().Active() {
		t.Fatal("the fixture is not Active to begin with")
	}
	disabled := base()
	disabled.Enabled = false
	if disabled.Active() {
		t.Error("Enabled=false did not deactivate")
	}
	unmet := base()
	unmet.Requires = Requires{CommandOnPath: "definitely-not-a-real-binary-xyz", CommandOnPathSet: true}
	if unmet.Active() {
		t.Error("an unmet requirement did not deactivate")
	}
	superseded := base()
	applySupersessions([]*Loophole{superseded}, []PackSupersession{
		{Pack: "p", Capability: "job-a", Because: "moot"}})
	if superseded.Active() {
		t.Error("supersession did not deactivate")
	}
	// And the ORDER of the explanation: a superseded loophole whose requirement is ALSO
	// unmet must say "superseded", because that is the fact the reader can act on —
	// reporting the probe would send them to install something that changes nothing.
	both := base()
	both.Requires = unmet.Requires
	applySupersessions([]*Loophole{both}, []PackSupersession{
		{Pack: "p", Capability: "job-a", Because: "moot"}})
	reason, _ := both.InactiveReason()
	if !strings.Contains(reason, "superseded") {
		t.Errorf("InactiveReason() = %q for a loophole that is both superseded and "+
			"missing a binary; supersession is the actionable one", reason)
	}
}

// TestSupersededLoopholeCrossesNothing is the point of the whole exercise (design §7):
// under supersession the in-jail terminator does not bind 127.0.0.1:443, no CA is
// trusted, no daemon is spawned. Both host-crossing surfaces gate on Active(), so this
// pins that the gate really is the one they read.
func TestSupersededLoopholeCrossesNothing(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	dir := mkdir(t, filepath.Join(root, "brokerish"))
	writeManifest(t, dir, map[string]any{
		"name": "brokerish", "description": "x",
		"transport":  "loopback-tls",
		"serves":     []string{"claude-oauth-refresh"},
		"intercepts": []map[string]any{{"host": "platform.claude.com"}},
		"broker_ip":  "127.0.0.1",
		// NO `jail_env`: the fixture is read through the pack loader (packs are the only
		// module source left) and the pack-shipped subset refuses it, which would make the
		// manifest vanish and every assertion below pass over an empty set.
		"host_daemon": map[string]any{
			"cmd": []string{"/bin/true", "--socket", "{socket}"}, "publishes": "socket"},
	})

	live := discoverWith(root, nil)
	if args := live.RuntimeArgsFor(live.All(), "podman"); len(args) == 0 {
		t.Fatal("the fixture contributes no runtime args even when live")
	}
	if specs := live.ManifestHostDaemonSpecs(live.All()); specs.Len() != 1 {
		t.Fatal("the fixture contributes no host daemon even when live")
	}

	off := discoverWith(root, []PackSupersession{bedrock()})
	if args := off.RuntimeArgsFor(off.All(), "podman"); len(args) != 0 {
		t.Errorf("a superseded loophole still contributes container args: %v — the "+
			"--add-host and the CA are exactly what §7 removes", args)
	}
	if specs := off.ManifestHostDaemonSpecs(off.All()); specs.Len() != 0 {
		t.Errorf("a superseded loophole's host daemon is still in the spawn list: %v",
			specs.Keys())
	}
	if len(off.Active()) != 0 || len(off.Honored()) != 0 {
		t.Error("a superseded loophole is still in the Active/Honored views")
	}
	// Still DISCOVERED and still listed, exactly like an unapproved pack loophole: an
	// absence is indistinguishable from a loophole that failed to load, and the fix is
	// not discoverable from nothing.
	if _, ok := off.Lookup("brokerish"); !ok {
		t.Error("a superseded loophole vanished from the set instead of being reported")
	}
}

// ---------------------------------------------------------------------------
// R7 — the typo failure mode
// ---------------------------------------------------------------------------

// TestUnmatchedSupersessionIsReported is design §5's first engineered-out failure:
// a typo supersedes nothing, silently, and the author believes it worked.
//
// REPORTED, not refused — see unmatchedSupersessions for the three reasons the
// design's "refused at load" cannot hold for the MATCH half. What is asserted here is
// the part that carries the value: the unmatched string, a did-you-mean, and the list
// of what IS served.
func TestUnmatchedSupersessionIsReported(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})
	warnings := captureWarnings(t)

	typo := PackSupersession{Pack: "claude-bedrock", Capability: "claude-oauth-refersh",
		Because: "Bedrock"}
	set := discoverWith(root, []PackSupersession{typo})

	lp, _ := set.Lookup("broker-like")
	if lp.Superseded() || !lp.Active() {
		t.Fatal("a misspelled capability superseded something")
	}
	joined := strings.Join(*warnings, "\n")
	for _, want := range []string{
		"claude-oauth-refersh", // the unmatched string
		"did you mean",         // the fix
		"claude-oauth-refresh", // the suggestion AND the served list
		"claude-bedrock",       // who claimed it
		"keeps running",        // what actually happened
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q missing %q — \"superseded nothing\" is useless; the "+
				"message is most of the value here", joined, want)
		}
	}
	// The same content, value-shaped, for a surface that wants to render it itself.
	probs := set.SupersessionProblems()
	if len(probs) != 1 || !strings.Contains(probs[0], "claude-oauth-refersh") {
		t.Errorf("SupersessionProblems() = %v", probs)
	}
}

// TestUnmatchedSupersessionWithNothingServed: with no `serves` anywhere, a
// did-you-mean would be a guess, so the message says what is true instead.
func TestUnmatchedSupersessionWithNothingServed(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "quiet", nil)
	captureWarnings(t)

	probs := discoverWith(root, []PackSupersession{bedrock()}).SupersessionProblems()
	if len(probs) != 1 {
		t.Fatalf("SupersessionProblems() = %v, want one", probs)
	}
	if strings.Contains(probs[0], "did you mean") {
		t.Errorf("%q guesses a suggestion when nothing is served", probs[0])
	}
	if !strings.Contains(probs[0], "declares `serves` at all") {
		t.Errorf("%q does not say that nothing here can be superseded", probs[0])
	}
}

// TestNearestCapabilityRefusesToGuess: a suggestion is a fix only when it is close.
// An unrelated name must not be offered, or the message teaches the wrong lesson.
func TestNearestCapabilityRefusesToGuess(t *testing.T) {
	served := []string{"claude-oauth-refresh", "audio-playback"}
	if got := nearestCapability("claude-oauth-refersh", served); got != "claude-oauth-refresh" {
		t.Errorf("nearestCapability(transposed) = %q, want the real name", got)
	}
	if got := nearestCapability("something-else-entirely", served); got != "" {
		t.Errorf("nearestCapability(unrelated) = %q, want no suggestion", got)
	}
	if got := nearestCapability("ab", served); got != "" {
		t.Errorf("nearestCapability(short) = %q, want no suggestion", got)
	}
}

// TestTwoPacksSupersedingOneCapability: not a conflict (design §5 — any supersession
// wins, deliberately no `needs`), and the winner is reported so the reader knows who
// to talk to.
func TestTwoPacksSupersedingOneCapability(t *testing.T) {
	unsetJail(t)
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})
	warnings := captureWarnings(t)

	set := discoverWith(root, []PackSupersession{
		bedrock(),
		{Pack: "other", Capability: "claude-oauth-refresh", Because: "also moot"},
	})
	lp, _ := set.Lookup("broker-like")
	if !lp.Superseded() {
		t.Fatal("two claims on one capability superseded nothing")
	}
	if len(lp.SupersededBy) != 1 || lp.SupersededBy[0].Pack != "claude-bedrock" {
		t.Errorf("SupersededBy = %v, want the first claim only", lp.SupersededBy)
	}
	if len(set.SupersessionProblems()) != 0 || len(*warnings) != 0 {
		t.Errorf("two packs superseding one capability was reported as a problem: %v %v",
			set.SupersessionProblems(), *warnings)
	}
}

// ---------------------------------------------------------------------------
// The process-wide record
// ---------------------------------------------------------------------------

// TestPackSupersessionRecordIsFailSafe: with nothing recorded nothing is superseded,
// and the explicit record beats the lazy resolver — the same two-tier contract
// SetPackModules has, and here the empty branch is the SAFE one (a loophole keeps
// running) rather than merely the cautious one.
func TestPackSupersessionRecordIsFailSafe(t *testing.T) {
	t.Cleanup(ResetPackSupersessions)
	ResetPackSupersessions()
	if got := PackSupersessions(); len(got) != 0 {
		t.Fatalf("PackSupersessions() = %v with nothing recorded", got)
	}

	resolved := 0
	SetPackSupersessionResolver(func() []PackSupersession {
		resolved++
		return []PackSupersession{{Pack: "lazy", Capability: "job-a", Because: "b"}}
	})
	t.Cleanup(func() { SetPackSupersessionResolver(nil) })
	if got := PackSupersessions(); len(got) != 1 || got[0].Pack != "lazy" {
		t.Fatalf("PackSupersessions() = %v, want the resolver's answer", got)
	}
	if PackSupersessions(); resolved != 1 {
		t.Errorf("resolver ran %d times; it must be memoized", resolved)
	}

	SetPackSupersessions([]PackSupersession{{Pack: "staged", Capability: "job-a", Because: "b"}})
	if got := PackSupersessions(); len(got) != 1 || got[0].Pack != "staged" {
		t.Errorf("PackSupersessions() = %v, want the staged record to supersede the resolver", got)
	}
	// Reset clears the staged record AND the resolver's CACHE, but leaves the resolver
	// installed — the same contract ResetPackModules has, so a test that installed one
	// sees it answer again rather than silently getting an empty set.
	ResetPackSupersessions()
	if got := PackSupersessions(); len(got) != 1 || got[0].Pack != "lazy" {
		t.Errorf("PackSupersessions() = %v after Reset, want the resolver to answer again", got)
	}
	SetPackSupersessionResolver(nil)
	ResetPackSupersessions()
	if got := PackSupersessions(); len(got) != 0 {
		t.Errorf("PackSupersessions() = %v with neither a record nor a resolver", got)
	}
}

// ---------------------------------------------------------------------------
// R5 — the user-visible report
// ---------------------------------------------------------------------------

// TestListNamesThePackAndTheReason: `yolo loopholes list` must say WHO turned it off
// and WHY (design §5), and it must not shorten the label into something that reads
// like an unmet requirement.
func TestListNamesThePackAndTheReason(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Cleanup(ResetPackSupersessions)
	captureWarnings(t)

	servingLoophole(t, onlyModules(t), "broker-like",
		[]string{"claude-oauth-refresh"})
	SetPackSupersessions([]PackSupersession{bedrock()})

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home,
		LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil }}
	if rc := List(deps); rc != 0 {
		t.Fatalf("List rc = %d, err=%q", rc, errBuf.String())
	}
	got := out.String()
	for _, want := range []string{"inactive (superseded)", "broker-like",
		"claude-bedrock", "claude-oauth-refresh", "no token is ever refreshed"} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
}

// TestStatusLabelsSuperseded: the other command a user reaches for. `superseded` is
// its own prefix rather than `inactive`, for the same reason the platform axis is its
// own — the two have different fixes, and one of them has no fix at all.
func TestStatusLabelsSuperseded(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Cleanup(ResetPackSupersessions)
	captureWarnings(t)

	servingLoophole(t, onlyModules(t), "broker-like",
		[]string{"claude-oauth-refresh"})
	SetPackSupersessions([]PackSupersession{bedrock()})

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home,
		LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil }}
	if rc := Status(deps); rc != 0 {
		t.Fatalf("Status rc = %d, err=%q", rc, errBuf.String())
	}
	got := out.String()
	if !strings.Contains(got, "[superseded] broker-like") {
		t.Errorf("status output has no superseded prefix:\n%s", got)
	}
	if !strings.Contains(got, "claude-bedrock") || !strings.Contains(got, "no token is ever refreshed") {
		t.Errorf("status output does not name who and why:\n%s", got)
	}
}

// TestValidateSetAppliesSupersession: `yolo check`'s walker builds its records
// directly (it needs the error channel Discover throws away), so the supersession pass
// has to run there too — otherwise it is the one census site that reports a superseded
// loophole as live, which is exactly the seven-surfaces disagreement Set was factored
// to end.
func TestValidateSetAppliesSupersession(t *testing.T) {
	unsetJail(t)
	t.Cleanup(ResetPackSupersessions)
	captureWarnings(t)
	servingLoophole(t, onlyModules(t), "broker-like", []string{"claude-oauth-refresh"})
	SetPackSupersessions([]PackSupersession{bedrock()})

	_, set := ValidateSet()
	lp, ok := set.Lookup("broker-like")
	if !ok {
		t.Fatal("loophole not in the validate set")
	}
	if !lp.Superseded() {
		t.Error("ValidateSet's records carry no supersession — `yolo check` would report a " +
			"retired loophole as live")
	}
	if len(set.SupersessionProblems()) != 0 {
		t.Errorf("SupersessionProblems() = %v for a matched claim", set.SupersessionProblems())
	}
}

// TestNarrowedSetKeepsTheClaims: a view of a Set must keep its supersession claims for
// the same reason it keeps the origin gate — a narrowed view answering "no problems"
// from a claim list that was simply not copied is a silent false negative, in the one
// direction this report exists to catch.
func TestNarrowedSetKeepsTheClaims(t *testing.T) {
	unsetJail(t)
	captureWarnings(t)
	root := modsDir(t)
	servingLoophole(t, root, "broker-like", []string{"claude-oauth-refresh"})

	full := discoverWith(root, []PackSupersession{
		{Pack: "p", Capability: "typo-job", Because: "b"}})
	if len(full.SupersessionProblems()) != 1 {
		t.Fatalf("the full set reports %v", full.SupersessionProblems())
	}
	narrowed := SetOf(full.Enabled()).withGate(full)
	if len(narrowed.SupersessionProblems()) != 1 {
		t.Errorf("SupersessionProblems() = %v after narrowing, want the claim carried over",
			narrowed.SupersessionProblems())
	}
}
