package run

// jailbriefingaudience_test.go is the JAIL NOTCH's gate on docs/reference/agent-briefings.md#audiences-what-varies-per-destination —
// the half #where-each-notch-narrows describes, which the retired design called "a structural move".
//
// WHY IT IS A DIFFERENT TEST FROM THE HOST NOTCH'S. The two notches route an audience by
// completely different mechanisms, which design risk R3 is about: the host resolves a
// destination for the addressed contribution (packload.borrowedDestinations) and then composes
// per destination, while the jail never calls ResolveDestinations at all — it composes ONE
// body per destination out of the whole pack set, filtering each pack's prose by that
// destination's declared identity. So an audience honored at the host says nothing about the
// jail, and vice versa. Deleting the `d.Agent` argument at refreshJailBriefings' compose call
// leaves every host-notch test green.
//
// EVERY TEST GOES THROUGH refreshJailBriefings, and TestJailBriefingStagingNameAgreesWithTheMount
// additionally through assembleRunCmd, because the two halves that must agree about the staging
// filename live in different functions and a mismatch is SILENT (a missing bind source for a
// FILE is not an error — the jail just comes up with a blank briefing). That is R2.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jailPack builds an in-memory pack: the manifest contributions given, plus any files.
func jailPack(t *testing.T, name string, files map[string]string,
	contributes ...packdecl.Contribution) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &packload.Pack{Name: name, Root: root,
		Decl: &packdecl.Manifest{Contributes: contributes}}
}

// jailDest is an agent pack: it names where its agent reads and the identity that
// destination answers to.
func jailDest(t *testing.T, name, into, agent string) *packload.Pack {
	t.Helper()
	return jailPack(t, name, nil, packdecl.Contribution{
		Kind: packdecl.KindBriefing, Into: into, Agent: agent})
}

// jailBriefings runs the REAL refreshJailBriefings over a hand-built pack set and returns
// {home-relative destination → the bytes written to its staging file}, read back through the
// same briefingStagingName the mount half uses.
func jailBriefings(t *testing.T, packs []*packload.Pack,
	proses []jailcontent.PackBriefing) map[string]string {
	t.Helper()
	return jailBriefingsWith(t, jsonx.NewOrderedMap(), packs, proses)
}

// jailBriefingsWith is jailBriefings under a given effective config.
func jailBriefingsWith(t *testing.T, cfg *jsonx.OrderedMap, packs []*packload.Pack,
	proses []jailcontent.PackBriefing) map[string]string {
	t.Helper()
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	o := goldenOptions(ws, home)
	o.Stdout = discardBuf()
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, "podman",
		stagedPacks{packs: packs, briefings: proses})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	out := map[string]string{}
	for _, d := range briefingDestinations(packs) {
		data, rerr := os.ReadFile(filepath.Join(staging, briefingStagingName(d.Into)))
		if rerr != nil {
			t.Fatalf("no briefing staged for %s: %v", d.Into, rerr)
		}
		out[d.Into] = string(data)
	}
	return out
}

// THE HEADLINE ASSERTION, and the one that was impossible before this change: addressed prose
// reaches the destination whose owner declared that identity and NO other. Two agent packs, so
// "no other" is a measurement.
func TestJailBriefingDeliversAddressedProseOnlyToItsAudience(t *testing.T) {
	got := jailBriefings(t,
		[]*packload.Pack{
			jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
			jailDest(t, "codex", ".codex/AGENTS.md", "codex"),
		},
		[]jailcontent.PackBriefing{
			{Name: "house", Text: "Claude-only rule.", Agents: []string{"claude"}},
		})

	if !strings.Contains(got[".claude/CLAUDE.md"], "Claude-only rule.") {
		t.Errorf("the addressed prose never reached claude's briefing:\n%s", got[".claude/CLAUDE.md"])
	}
	if strings.Contains(got[".codex/AGENTS.md"], "Claude-only rule.") {
		t.Errorf("claude-addressed prose was broadcast into codex's briefing — a jail composed "+
			"ONE body and wrote it everywhere, which is the defect this move closes:\n%s",
			got[".codex/AGENTS.md"])
	}
	// The base briefing is still every destination's.
	for dest, body := range got {
		if !strings.Contains(body, "Jail Environment") {
			t.Errorf("%s lost yolo's own briefing — only the PACK prose is scoped:\n%s", dest, body)
		}
	}
	// UNLABELLED BY DEFAULT: an addressed section is plain prose like any other, and the launch
	// pipeline must not re-add the label briefing_provenance turned off.
	if strings.Contains(got[".claude/CLAUDE.md"], "<!-- from pack:") {
		t.Errorf("a default launch labelled pack prose:\n%s", got[".claude/CLAUDE.md"])
	}
}

// P2: SILENCE MEANS BROADCAST. Prose that names no audience still reaches every destination —
// the property that makes the field safe to land ahead of any pack adopting it, and the one a
// filter applied too eagerly breaks.
func TestJailBriefingStillBroadcastsUnaudiencedProse(t *testing.T) {
	got := jailBriefings(t,
		[]*packload.Pack{
			jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
			jailDest(t, "codex", ".codex/AGENTS.md", "codex"),
		},
		[]jailcontent.PackBriefing{{Name: "house", Text: "Everyone's rule."}})

	for _, dest := range []string{".claude/CLAUDE.md", ".codex/AGENTS.md"} {
		if !strings.Contains(got[dest], "Everyone's rule.") {
			t.Errorf("%s lost the broadcast prose:\n%s", dest, got[dest])
		}
	}
}

// R4: a destination that declares NO identity receives every broadcast and no addressed prose.
// Not an error — it is the state every pack.json was in before the field existed.
func TestJailBriefingSkipsADestinationWithNoDeclaredIdentity(t *testing.T) {
	got := jailBriefings(t,
		[]*packload.Pack{
			jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
			jailPack(t, "silent", nil, packdecl.Contribution{
				Kind: packdecl.KindBriefing, Into: ".silent/AGENTS.md"}),
		},
		[]jailcontent.PackBriefing{
			{Name: "house", Text: "Claude-only rule.", Agents: []string{"claude"}},
			{Name: "house", Text: "Everyone's rule."},
		})

	if strings.Contains(got[".silent/AGENTS.md"], "Claude-only rule.") {
		t.Errorf("an addressed selector reached a destination with nothing to match against:\n%s",
			got[".silent/AGENTS.md"])
	}
	if !strings.Contains(got[".silent/AGENTS.md"], "Everyone's rule.") {
		t.Errorf("an unaddressable destination must still get the broadcast — declaring no "+
			"identity makes a destination unaddressable, not unbriefed:\n%s",
			got[".silent/AGENTS.md"])
	}
}

// THE LIMIT THE DESTINATION-FIRST MOVE LIFTS FOR FREE (agent-briefings.md#where-each-notch-narrows), end to end through the launch path: a pack declaring TWO
// briefing contributions with two different `from` files now delivers BOTH into a jail.
//
// packload.BriefingProse recorded this as a live limit — "the jail's composition takes one
// (pack, text) pair per pack … the host render is per-DESTINATION and does honor both; making
// the jail match would mean composing per destination, which is a larger change". This test
// drives stagePacks, so it fails if packBriefingProses reverts to that per-pack reader.
func TestJailBriefingDeliversBothOfAPacksTwoProseFiles(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "two")
	if err := os.MkdirAll(filepath.Join(packDir, "prose"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"name":"two","contributes":[`+
		`{"kind":"briefing","from":"prose/first.md","into":".claude/CLAUDE.md"},`+
		`{"kind":"briefing","from":"prose/second.md","into":".codex/AGENTS.md"}]}`)
	for rel, body := range map[string]string{
		"prose/first.md":  "First file's prose.\n",
		"prose/second.md": "Second file's prose.\n",
	} {
		if err := os.WriteFile(filepath.Join(packDir, filepath.FromSlash(rel)),
			[]byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"two"}]`)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf()}
	_, _, proses, err := o.stagePacks("yolo-test-twoprose")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	var texts []string
	for _, p := range proses {
		texts = append(texts, p.Text)
	}
	joined := strings.Join(texts, "|")
	for _, want := range []string{"First file's prose.", "Second file's prose."} {
		if !strings.Contains(joined, want) {
			t.Errorf("the jail path dropped %q — a pack's SECOND briefing contribution used to "+
				"be unreachable in a jail (packload.BriefingProse's recorded limit), and lifting "+
				"it is what the destination-first move (agent-briefings.md#where-each-notch-narrows) buys for free; got: %v",
				want, texts)
		}
	}
}

// TWO CONTRIBUTIONS NAMING ONE SOURCE ARE A LAUNCH REFUSAL (docs/reference/pack-system.md#oq-pb5).
// This used to be a dedup: two `into`s and no `from` both resolved to AGENTS.md, and the reader
// composed it once. Under per-file governance a file has exactly ONE governor, so the pair is
// refused naming both — and its one-contribution spelling (silence: every agent) composes each
// file exactly once into every destination.
func TestJailBriefingComposesIdenticalProseOnce(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "twice")
	if err := os.MkdirAll(filepath.Join(packDir, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, packDir, `{"name":"twice","contributes":[`+
		`{"kind":"briefing","into":".claude/CLAUDE.md"},`+
		`{"kind":"briefing","into":".codex/AGENTS.md"}]}`)
	if err := os.WriteFile(filepath.Join(packDir, "briefing", "rule.md"),
		[]byte("Only rule.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[{"source":"file://`+packDir+`","name":"twice"}]`)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf()}
	_, _, _, err := o.stagePacks("yolo-test-twice")
	if err == nil || !strings.Contains(err.Error(), "contributes[1]") ||
		!strings.Contains(err.Error(), "contributes[0]") {
		t.Fatalf("stagePacks err = %v, want the OQ-PB5 refusal naming both contributions", err)
	}

	// The one-contribution spelling, beside two agent packs.
	writePack(t, packDir, `{"name":"twice","contributes":[{"kind":"briefing"}]}`)
	_, packs, proses, err := o.stagePacks("yolo-test-twice")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	if len(proses) != 1 || len(proses[0].Agents) != 0 {
		t.Fatalf("want ONE broadcast prose entry for one file, got %+v", proses)
	}
	packs = append(packs, jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
		jailDest(t, "codex", ".codex/AGENTS.md", "codex"))
	got := jailBriefings(t, packs, proses)
	if len(got) != 2 {
		t.Fatalf("destinations = %v, want claude's and codex's", got)
	}
	for dest, body := range got {
		if n := strings.Count(body, "Only rule."); n != 1 {
			t.Errorf("%s carries the pack's prose %d times, want once:\n%s", dest, n, body)
		}
	}
}

// THE JAIL CALL SITE FOR PER-FILE GOVERNANCE — the matt shape (docs/reference/pack-system.md#one-governance-reader,
// pack-system.md#briefing-governance) through the real stagePacks and refreshJailBriefings. House rules under briefing/, plus
// ONE addressed file for pi: Claude must get the house rules (the broadcast the old `if !declared`
// branch switched off the moment the addressed line was added), and pi must get both, in filename
// order, as one section. The root AGENTS.md — the pack repository's own guide — reaches neither.
//
// Mutation (reported): restoring an `if !declared` gate in packBriefingProses, so the implicit
// broadcast is read only when the pack declares no briefing, drops the house rules and fails here.
func TestJailBriefingMattShape(t *testing.T) {
	home := packHome(t)
	packDir := filepath.Join(t.TempDir(), "matt")
	for rel, body := range map[string]string{
		"briefing/house-rules.md": "House rules.\n",
		"files/pi-rules.md":       "Pi rules.\n",
		"AGENTS.md":               "Contributor guide for the matt repository.\n",
	} {
		full := filepath.Join(packDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePack(t, packDir, `{"contributes":[`+
		`{"kind":"briefing","agents":["pi"],"from":"files/pi-rules.md"}]}`)
	// The SHIPPED claude and pi packs, so the audience pre-flight has a pi to find and the
	// destinations are the real ones. Both carry `after: "host:…"`, so the jail side is pinned:
	// in CI the launcher is on the host and in here it is in a jail (briefingreadback.go).
	writeUserPacks(t, home, `["claude","pi",{"source":"file://`+packDir+`","name":"matt"}]`)
	pinLauncherInJail(t, false)

	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf()}
	_, packs, proses, err := o.stagePacks("yolo-test-matt")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	got := jailBriefings(t, packs, proses)

	claude, pi := got[".claude/CLAUDE.md"], got[".pi/agent/AGENTS.md"]
	if !strings.Contains(claude, "House rules.") {
		t.Errorf("claude's briefing lost the house rules — declaring a narrower briefing "+
			"switched the broadcast off:\n%s", claude)
	}
	if strings.Contains(claude, "Pi rules.") {
		t.Errorf("pi-addressed prose reached claude:\n%s", claude)
	}
	if !strings.Contains(pi, "House rules.\n\nPi rules.\n") {
		t.Errorf("pi's briefing = %q, want the house rules then pi-rules as one section", pi)
	}
	for dest, body := range got {
		if strings.Contains(body, "Contributor guide") {
			t.Errorf("%s carries the pack repository's AGENTS.md — never pack prose (P1)", dest)
		}
	}
}

// R2, AND THE ONE ASSERTION NO UNIT TEST OF EITHER HALF CAN MAKE: the file the mount names is
// the file the write half produced.
//
// This is the skew the staging-key change could introduce — assembleRunCmd emitting a name
// refreshJailBriefings no longer writes. It does not fail the launch: podman happily binds a
// nonexistent FILE source, so the jail comes up and its agent reads nothing. So the assertion
// is on the FILESYSTEM, after both halves have run over one pack set.
func TestJailBriefingStagingNameAgreesWithTheMount(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	jailcontent.SetPackSkillDirs(nil)
	jailcontent.SetPackSkillTargets(nil)
	t.Cleanup(func() { jailcontent.SetPackSkillDirs(nil); jailcontent.SetPackSkillTargets(nil) })

	packs := []*packload.Pack{
		jailDest(t, "claude", ".claude/CLAUDE.md", "claude"),
		jailDest(t, "codex", ".codex/AGENTS.md", "codex"),
	}
	o := goldenOptions(ws, home)
	o.Stdout = discardBuf()
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", jsonx.NewOrderedMap(), "podman",
		stagedPacks{packs: packs})
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}

	in := relocationInput(t, "podman", filepath.Join(ws, ".yolo", "home"), nil)
	in.packs = packs
	in.agentsPath = staging
	argv := o.assembleRunCmd(in)

	var mounts int
	for _, a := range argv {
		src, rest, ok := strings.Cut(a, ":/home/agent/")
		if !ok || !strings.HasSuffix(rest, ":ro") {
			continue
		}
		if !strings.Contains(filepath.Base(src), "briefing-") {
			continue
		}
		mounts++
		if _, serr := os.Stat(src); serr != nil {
			t.Errorf("the argv mounts a briefing staging file that was never written: %s\n"+
				"assembleRunCmd and refreshJailBriefings must agree about the staging name; a "+
				"mismatch is SILENT (podman binds a missing FILE source happily) and the jail "+
				"comes up with a blank briefing", src)
		}
	}
	if mounts != 2 {
		t.Fatalf("want one briefing mount per destination (2), got %d — this test asserts "+
			"nothing if the mounts are not on the argv", mounts)
	}
}

// The staging name must be INJECTIVE, because two destinations sharing a staging file would
// deliver one agent's composed briefing to the other, silently. The `~` escape is the whole
// reason `/`-to-`_` was not used: `a/b` and `a_b` are different destinations.
func TestBriefingStagingNameIsInjective(t *testing.T) {
	dests := []string{
		".claude/CLAUDE.md", ".codex/AGENTS.md", ".config/opencode/AGENTS.md",
		"a/b", "a~b", "a~~b", "a/~b", "a~/b",
	}
	seen := map[string]string{}
	for _, d := range dests {
		name := briefingStagingName(d)
		if prev, dup := seen[name]; dup {
			t.Errorf("destinations %q and %q share the staging file %q — one agent's briefing "+
				"would silently be delivered to the other", prev, d, name)
		}
		seen[name] = d
		if strings.Contains(name, "/") {
			t.Errorf("staging name %q for %q contains a path separator, so it is not a "+
				"filename at all", name, d)
		}
	}
}

// THE CALL SITE, pinned: `briefing_provenance: true` in the effective config must reach the
// composer through refreshJailBriefings. The composer's own tests cover both values; this is the
// test that fails if the launch stops reading the key and hard-codes the default.
func TestJailBriefingLabelsPackProseWhenTheConfigAsks(t *testing.T) {
	packs := []*packload.Pack{jailDest(t, "claude", ".claude/CLAUDE.md", "claude")}
	proses := []jailcontent.PackBriefing{{Name: "house", Text: "House rule."}}

	cfg := jsonx.NewOrderedMap()
	cfg.Set("briefing_provenance", true)
	on := jailBriefingsWith(t, cfg, packs, proses)[".claude/CLAUDE.md"]
	if !strings.Contains(on, "<!-- from pack: house -->\nHouse rule.") {
		t.Errorf("briefing_provenance: true did not label the pack's prose:\n%s", on)
	}

	off := jailBriefings(t, packs, proses)[".claude/CLAUDE.md"]
	if strings.Contains(off, "<!-- from pack:") || !strings.Contains(off, "House rule.") {
		t.Errorf("the default must deliver the prose unlabelled:\n%s", off)
	}
}
