package entrypoint

// hostbriefingaudience_test.go is the HOST NOTCH's own gate on briefing-audiences.md — design
// risk R3, which asks for the assertion per notch because §5 shows the two notches change
// differently.
//
// WHAT IT PINS, AND WHERE THE FILTER ACTUALLY IS. §5 describes the host half as "a filter" in
// ComposeHostBriefings, beside the `prose == ""` skip. Measured against the tree, that filter
// is not needed and must not be added: an addressed contribution carries no `into`, so it
// never reaches the per-destination loop at all — the narrowing already happened upstream, in
// packload's borrowedDestinations, which is the same filter §4.1 asks for. The host notch's
// contribution is the PAIRING: ResolveDestinations, then compose. So every test here drives
// that pairing, exactly as internal/cli/apply.go does, rather than either half alone. Delete
// the audience check in borrowedDestinations and these go red — which is the point, since a
// test of ComposeHostBriefings alone would stay green with the whole feature switched off.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// identityPack is an AGENT pack: it names the file its agent reads AND declares the identity
// that file answers to, which is the pair §4.1's first block describes.
func identityPack(t *testing.T, name, into, agent string) *packload.Pack {
	t.Helper()
	return &packload.Pack{
		Name: name, Root: t.TempDir(),
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindBriefing, Into: into, Agent: agent,
		}}},
	}
}

// addressedPack is a CONTENT pack: it names WHO its prose is for and no path at all (P4).
// `agents` empty builds the unaudienced broadcast contribution instead, which is what P2
// promises keeps working.
func addressedPack(t *testing.T, name, from, prose string, agents ...string) *packload.Pack {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(from))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(prose), 0o644); err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{
		Name: name, Root: root,
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindBriefing, From: from, Agents: agents,
		}}},
	}
}

// composeAudienced runs the production pairing — resolve, then compose — and returns
// {home-relative destination → composed content}.
func composeAudienced(t *testing.T, home string, packs ...*packload.Pack) map[string]string {
	t.Helper()
	resolved, _ := packload.ResolveDestinations(packs)
	out := map[string]string{}
	for _, d := range ComposeHostBriefings(resolved, home) {
		rel, err := filepath.Rel(home, d.Path)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.ToSlash(rel)] = d.Content
	}
	return out
}

// THE HEADLINE ASSERTION: addressed prose reaches the destination its audience names and is
// ABSENT from every other one. Two agent packs, so "absent" is a real measurement rather than
// the trivial truth of a one-destination jail.
func TestHostNotchDeliversAddressedProseOnlyToItsAudience(t *testing.T) {
	got := composeAudienced(t, t.TempDir(),
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		identityPack(t, "codex", ".codex/AGENTS.md", "codex"),
		addressedPack(t, "house", "prose/claude.md", "Claude-only rule.\n", "claude"))

	if !strings.Contains(got[".claude/CLAUDE.md"], "Claude-only rule.") {
		t.Errorf("the addressed prose did not reach the destination it named:\n%q",
			got[".claude/CLAUDE.md"])
	}
	if strings.Contains(got[".codex/AGENTS.md"], "Claude-only rule.") {
		t.Errorf("claude-addressed prose was broadcast to codex — the whole defect this "+
			"design closes:\n%q", got[".codex/AGENTS.md"])
	}
	// PROVENANCE IS UNCHANGED (§7 non-goal): the section is still attributed to its pack.
	if !strings.Contains(got[".claude/CLAUDE.md"], "<!-- from pack: house -->") {
		t.Errorf("an addressed section arrived unattributed:\n%q", got[".claude/CLAUDE.md"])
	}
}

// AND IT DELIVERS THE SOURCE IT NAMED. A pack shipping two files, one per agent, is §4.1's
// own two-entry example — and the case a per-KIND answer cannot express, because the union of
// the audiences would send both files to both agents.
func TestHostNotchDeliversEachAddressedSourceToItsOwnAudience(t *testing.T) {
	claudeOnly := "For claude only.\n"
	piOnly := "For pi only.\n"
	root := t.TempDir()
	for rel, body := range map[string]string{"prose/claude.md": claudeOnly, "prose/pi.md": piOnly} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	house := &packload.Pack{Name: "house", Root: root, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindBriefing, From: "prose/claude.md", Agents: []string{"claude"}},
			{Kind: packdecl.KindBriefing, From: "prose/pi.md", Agents: []string{"pi"}},
		}}}

	got := composeAudienced(t, t.TempDir(),
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		identityPack(t, "pi", ".pi/agent/AGENTS.md", "pi"),
		house)

	for dest, want := range map[string]string{
		".claude/CLAUDE.md":   claudeOnly,
		".pi/agent/AGENTS.md": piOnly,
	} {
		if !strings.Contains(got[dest], strings.TrimSpace(want)) {
			t.Errorf("%s is missing its own file's prose:\n%q", dest, got[dest])
		}
	}
	if strings.Contains(got[".claude/CLAUDE.md"], strings.TrimSpace(piOnly)) {
		t.Errorf("pi's file leaked into claude's briefing:\n%q", got[".claude/CLAUDE.md"])
	}
	if strings.Contains(got[".pi/agent/AGENTS.md"], strings.TrimSpace(claudeOnly)) {
		t.Errorf("claude's file leaked into pi's briefing:\n%q", got[".pi/agent/AGENTS.md"])
	}
}

// P2: SILENCE MEANS BROADCAST. An unaudienced contribution still reaches every destination —
// the property that makes the field safe to land ahead of any pack adopting it, and the one a
// filter applied too eagerly would break.
func TestHostNotchStillBroadcastsAnUnaudiencedContribution(t *testing.T) {
	got := composeAudienced(t, t.TempDir(),
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		identityPack(t, "codex", ".codex/AGENTS.md", "codex"),
		addressedPack(t, "house", "AGENTS.md", "Everyone's rule.\n"))

	for _, dest := range []string{".claude/CLAUDE.md", ".codex/AGENTS.md"} {
		if !strings.Contains(got[dest], "Everyone's rule.") {
			t.Errorf("%s lost the broadcast prose — a contribution with no `agents` behaves "+
				"exactly as it did before the field existed (P2):\n%q", dest, got[dest])
		}
	}
}

// A DESTINATION THAT DECLARES NO IDENTITY is never named by any selector, and that is not an
// error (R4) — it is the state every pack.json was in before the field. The addressed prose
// simply does not reach it, and the destination is still composed and still owned.
func TestHostNotchSkipsADestinationWithNoDeclaredIdentity(t *testing.T) {
	silent := &packload.Pack{Name: "silent", Root: t.TempDir(),
		Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindBriefing, Into: ".silent/AGENTS.md",
		}}}}
	got := composeAudienced(t, t.TempDir(),
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		silent,
		addressedPack(t, "house", "prose/claude.md", "Claude-only rule.\n", "claude"))

	if _, ok := got[".silent/AGENTS.md"]; !ok {
		t.Fatal("the identity-less destination vanished from the composition — a pack that " +
			"declares no `agent` is unaddressable, not unowned")
	}
	if strings.Contains(got[".silent/AGENTS.md"], "Claude-only rule.") {
		t.Errorf("an addressed selector reached a destination that declared nothing to match "+
			"against:\n%q", got[".silent/AGENTS.md"])
	}
}

// OWNERSHIP IS UNAFFECTED (§5, §7): a pack that SKIPS a destination does not become an owner
// of it. The `Packs` list is what the prune reads to decide a destination still has a
// contributor, so an addressed pack listed at every destination would keep a file alive that
// nothing writes to.
func TestHostNotchAddressedPackDoesNotOwnTheDestinationsItSkips(t *testing.T) {
	resolved, _ := packload.ResolveDestinations([]*packload.Pack{
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		identityPack(t, "codex", ".codex/AGENTS.md", "codex"),
		addressedPack(t, "house", "prose/claude.md", "Claude-only rule.\n", "claude"),
	})
	home := t.TempDir()
	owners := map[string][]string{}
	for _, d := range ComposeHostBriefings(resolved, home) {
		rel, _ := filepath.Rel(home, d.Path)
		owners[filepath.ToSlash(rel)] = d.Packs
	}
	if got := strings.Join(owners[".codex/AGENTS.md"], ","); got != "codex" {
		t.Errorf("codex's destination owners = %q, want just \"codex\": an addressed pack that "+
			"skips a destination is not an owner of it", got)
	}
	if got := strings.Join(owners[".claude/CLAUDE.md"], ","); got != "claude,house" {
		t.Errorf("claude's destination owners = %q, want \"claude,house\": the addressed pack "+
			"DOES contribute there, so the prune must see it as a contributor", got)
	}
}

// THE RESOLUTION REPORTS WHAT IT DID, for both outcomes — the input `yolo host apply
// --observe` prints. A matched delivery names its audience (otherwise the report says
// "declares no destination" about a pack that declared precisely who its prose was for), and
// an audience that matched nothing is recorded by NAME rather than only by kind, which is the
// half-truth the design's own build note records `Orphaned []Kind` leaving behind.
func TestResolutionRecordsWhatAnAddressedContributionReached(t *testing.T) {
	house := addressedPack(t, "house", "prose/claude.md", "Claude-only rule.\n", "claude")

	_, outcomes := packload.ResolveDestinations([]*packload.Pack{
		identityPack(t, "claude", ".claude/CLAUDE.md", "claude"),
		identityPack(t, "codex", ".codex/AGENTS.md", "codex"),
		house,
	})
	var matched *packload.AddressedDelivery
	for i := range outcomes {
		if outcomes[i].Pack.Name != "house" {
			continue
		}
		if len(outcomes[i].Addressed) != 1 {
			t.Fatalf("want one addressed record for one addressed contribution, got %+v",
				outcomes[i].Addressed)
		}
		matched = &outcomes[i].Addressed[0]
	}
	if matched == nil {
		t.Fatal("the addressed pack produced no resolution outcome")
	}
	if strings.Join(matched.Agents, ",") != "claude" || matched.From != "prose/claude.md" {
		t.Errorf("the record must carry the audience and the source verbatim; got %+v", matched)
	}
	if strings.Join(matched.Into, ",") != ".claude/CLAUDE.md" {
		t.Errorf("Into = %v, want exactly the destination the audience matched", matched.Into)
	}

	// And the R1 case: the same pack, with no pack owning the name it addresses.
	_, alone := packload.ResolveDestinations([]*packload.Pack{
		identityPack(t, "codex", ".codex/AGENTS.md", "codex"),
		house,
	})
	for _, o := range alone {
		if o.Pack.Name != "house" {
			continue
		}
		if len(o.Addressed) != 1 || len(o.Addressed[0].Into) != 0 {
			t.Fatalf("an audience that matched nothing must be recorded with an empty Into, so "+
				"the report can name the AUDIENCE rather than only the kind; got %+v", o.Addressed)
		}
		if len(o.Orphaned) != 1 || o.Orphaned[0] != packdecl.KindBriefing {
			t.Errorf("the kind-level orphan signal every existing reader keys on must survive; "+
				"got %v", o.Orphaned)
		}
	}
}
