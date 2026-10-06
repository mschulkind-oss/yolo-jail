package packload

// envshadow_test.go pins OQ-NC12's DISCLOSURE (envshadow.go, ruled 2026-10-05): one line per
// variable for which one of yolo's own sources beat another that set it otherwise, naming the
// winner and every loser and never a value; nothing when nothing is shadowed. Each vehicle's own
// pin, which fails when its call site is deleted, is in internal/cli/run (the jail arms) and
// internal/cli (the host).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// winnerValues is every value winnerScope composes, none of which a shadow line may carry.
var winnerValues = []string{"shape", "shape-key", "es-claimed", "es-unclaimed", "fold-static", "gated"}

// requireNoValue fails for a line carrying one of values.
func requireNoValue(t *testing.T, lines []string, values []string) {
	t.Helper()
	for _, l := range lines {
		_, detail, _ := strings.Cut(l, ": ")
		for _, v := range values {
			for _, word := range strings.FieldsFunc(detail, func(r rune) bool { return r == ' ' || r == ',' || r == ';' }) {
				if word == v {
					t.Errorf("a shadow line printed the value %q: %s", v, l)
				}
			}
		}
	}
}

// THE JAIL'S LINES, over every process the launch composes for: a clause that holds for fxa's
// process alone names fxa; one that holds for every process names none, unless fxa's differs
// (K7), when it says "for every other process". K8 (one source) and K9 (a removal of nothing)
// are not shadowed.
func TestTheJailShadowLinesNameEachWinnerAndEveryLoser(t *testing.T) {
	got := winnerScope(t).ShadowLines(nil)
	want := []string{
		"Shadowed FXP_KEY: the fxp profile's value wins over your env_sources value, for fxa",
		"Shadowed K1: the fxp profile's value wins over the fx pack's value, for fxa",
		"Shadowed K2: the fxp profile's value wins over your env_sources value, for fxa",
		"Shadowed K4: your env_sources value wins over the fx pack's value",
		"Shadowed K5: your env_sources removal wins over the fx pack's value",
		"Shadowed K6: the fxp profile's value wins over your env_sources removal, for fxa",
		"Shadowed K7: the fxp profile's removal wins over your env_sources value and the fx pack's value, " +
			"for fxa; your env_sources value wins over the fx pack's value, for every other process",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ShadowLines:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	requireNoValue(t, got, winnerValues)
}

// THE HOST'S LINES speak for the one process it composes, so no clause names a process, and the
// lines are the jail's otherwise.
func TestTheHostShadowLinesSpeakForItsOneProcess(t *testing.T) {
	got := winnerScope(t).ShadowLinesFor("fxa", nil)
	want := []string{
		"Shadowed FXP_KEY: the fxp profile's value wins over your env_sources value",
		"Shadowed K1: the fxp profile's value wins over the fx pack's value",
		"Shadowed K2: the fxp profile's value wins over your env_sources value",
		"Shadowed K4: your env_sources value wins over the fx pack's value",
		"Shadowed K5: your env_sources removal wins over the fx pack's value",
		"Shadowed K6: the fxp profile's value wins over your env_sources removal",
		"Shadowed K7: the fxp profile's removal wins over your env_sources value and the fx pack's value",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ShadowLinesFor:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	requireNoValue(t, got, winnerValues)
	// A program no profile selects receives the shared composition, and its lines are that one's.
	if got := winnerScope(t).ShadowLinesFor("bash", nil); len(got) != 3 ||
		!strings.HasPrefix(got[0], "Shadowed K4: ") || !strings.HasPrefix(got[1], "Shadowed K5: ") ||
		!strings.HasPrefix(got[2], "Shadowed K7: your env_sources value wins over the fx pack's value") {
		t.Errorf("bash's lines must be the shared composition's K4, K5 and K7:\n%s", strings.Join(got, "\n"))
	}
}

// QUIET WHEN NOTHING IS SHADOWED: one source per name, a loser that gives the process what the
// winner gives it (the same value, a removal of nothing), two entries of one rank (EnvFold's own
// per-pack order), and a name the vehicle writes over the composition (except) print nothing.
func TestNothingShadowedPrintsNothing(t *testing.T) {
	var c EnvComposition
	c.put(EnvEntry{Key: "SAME", Value: "v", Origin: FromPackEnv, Pack: "a"})
	c.put(EnvEntry{Key: "SAME", Value: "v", Origin: FromEnvSources})
	c.put(EnvEntry{Key: "NOTHING", Value: "", Origin: FromPackEnv, Pack: "a"})
	c.put(EnvEntry{Key: "NOTHING", Unset: true, Origin: FromEnvSources})
	c.put(EnvEntry{Key: "RANK", Value: "1", Origin: FromPackEnv, Pack: "a"})
	c.put(EnvEntry{Key: "RANK", Value: "2", Origin: FromPackEnv, Pack: "b"})
	c.put(EnvEntry{Key: "ALONE", Value: "1", Origin: FromEnvSources})
	c.put(EnvEntry{Key: "TABLE", Value: "1", Origin: FromPackEnv, Pack: "a"})
	c.put(EnvEntry{Key: "TABLE", Value: "2", Origin: FromEnvSources})
	if got := shadowLines(shadowProcess{comp: c}, nil, []string{"TABLE"}); len(got) != 0 {
		t.Errorf("nothing is shadowed, yet the disclosure says:\n%s", strings.Join(got, "\n"))
	}
	if got := shadowLines(shadowProcess{comp: c}, nil, nil); len(got) != 1 || !strings.HasPrefix(got[0], "Shadowed TABLE: ") {
		t.Errorf("without except, TABLE is the one shadow: %v", got)
	}
	var nilScope *CredentialScope
	if nilScope.ShadowLines(nil) != nil || nilScope.ShadowLinesFor("fxa", nil) != nil {
		t.Error("a nil scope shadows nothing")
	}
	// A launch with no source competing for any name — winnerScope's fold and nothing else.
	scope, err := ScopeCredentials(ScopeInput{Packs: []*Pack{winnerPack(t)}, Providers: jsonx.NewOrderedMap()})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.ShadowLines(nil); len(got) != 0 {
		t.Errorf("a launch with only the fold shadows nothing:\n%s", strings.Join(got, "\n"))
	}
}

// ONE LINE PER NAME, however many processes shadow it: agents with one clause are grouped, and
// agents with different clauses share the line, the shared composition's clause last.
func TestOneLinePerNameGroupsTheAgents(t *testing.T) {
	compose := func(shape []agentenv.Var) EnvComposition {
		es := jsonx.NewOrderedMap()
		es.Set("K", "es")
		return composeEnv([]EnvFoldEntry{{Key: "K", Value: "fold", Pack: "p"}}, es, nil, shape, "p")
	}
	base := shadowProcess{comp: compose(nil)}
	a := shadowProcess{agent: "a", comp: compose([]agentenv.Var{{Key: "K", Value: "x"}}), d: &AgentDelivery{Profile: "zai", Set: []string{"zai"}}}
	b := shadowProcess{agent: "b", comp: compose([]agentenv.Var{{Key: "K", Value: "y"}}), d: &AgentDelivery{Profile: "zai", Set: []string{"zai"}}}
	c := shadowProcess{agent: "c", comp: compose([]agentenv.Var{{Key: "K", Value: "z"}}), d: &AgentDelivery{Profile: "kilo", Set: []string{"kilo"}}}
	got := shadowLines(base, []shadowProcess{a, b, c}, nil)
	want := "Shadowed K: the zai profile's value wins over your env_sources value and the p pack's value, for a and b; " +
		"the kilo profile's value wins over your env_sources value and the p pack's value, for c; " +
		"your env_sources value wins over the p pack's value, for every other process"
	if len(got) != 1 || got[0] != want {
		t.Errorf("lines:\n%s\nwant one:\n%s", strings.Join(got, "\n"), want)
	}
	requireNoValue(t, got, []string{"es", "fold", "x", "y", "z"})
}

// EACH SOURCE IS NAMED, never valued: an active set by its entries, a region the fill read by its
// file, a fold entry with no pack and a shape var with no delivery by what they are.
func TestEachShadowSourceIsNamedNeverValued(t *testing.T) {
	set := &AgentDelivery{Profile: "zai", Set: []string{"zai", "openrouter", "cerebras"}}
	region := &AgentDelivery{Profile: "bedrock", Set: []string{"bedrock"},
		RegionFile: &RegionFileLookup{Var: "AWS_REGION", Region: "eu-west-1", File: "/h/.aws/config", Home: "/h"}}
	for _, tc := range []struct {
		d    *AgentDelivery
		e    EnvEntry
		want string
	}{
		{set, EnvEntry{Key: "K", Value: "v", Origin: FromProfileEnv}, "the zai, openrouter and cerebras profiles' value"},
		{region, EnvEntry{Key: "AWS_REGION", Value: "eu-west-1", Origin: FromProfileEnv}, "the region yolo read from ~/.aws/config"},
		{region, EnvEntry{Key: "AWS_PROFILE", Value: "x", Origin: FromProfileEnv}, "the bedrock profile's value"},
		{region, EnvEntry{Key: "AWS_REGION", Unset: true, Origin: FromProfileEnv}, "the bedrock profile's removal"},
		{nil, EnvEntry{Key: "K", Value: "v", Origin: FromProfileEnv}, "the active profile's value"},
		{nil, EnvEntry{Key: "K", Value: "v", Origin: FromPackEnv}, "a selected pack's value"},
		{nil, EnvEntry{Key: "K", Value: "v", Origin: FromPackEnv, Pack: "aws-auth"}, "the aws-auth pack's value"},
		{nil, EnvEntry{Key: "K", Unset: true, Origin: FromEnvSources}, "your env_sources removal"},
	} {
		if got := shadowSource(shadowProcess{d: tc.d}, tc.e); got != tc.want {
			t.Errorf("shadowSource(%+v) = %q, want %q", tc.e, got, tc.want)
		}
	}
}

// THE COMPOSITION KEEPS WHAT EACH WINNER BEAT, by rank: a same-rank replacement drops its loser,
// a higher rank keeps it, the highest first.
func TestTheCompositionKeepsEachLowerRankedLoser(t *testing.T) {
	comp := winnerScope(t).EnvFor("fxa")
	losers := comp.Shadowed("K7")
	if len(losers) != 2 || losers[0].Origin != FromEnvSources || losers[1].Origin != FromPackEnv {
		t.Errorf("K7's losers = %+v, want env_sources then the fold", losers)
	}
	if got := comp.Shadowed("K8"); len(got) != 0 {
		t.Errorf("K8 has one source, yet Shadowed = %+v", got)
	}
	var c EnvComposition
	c.put(EnvEntry{Key: "K", Value: "1", Origin: FromPackEnv, Pack: "a"})
	c.put(EnvEntry{Key: "K", Value: "2", Origin: FromPackEnv, Pack: "b"})
	c.put(EnvEntry{Key: "K", Value: "3", Origin: FromEnvSources})
	if got := c.Shadowed("K"); len(got) != 1 || got[0].Pack != "b" {
		t.Errorf("the fold's own order keeps one loser, the fold's winner b: %+v", got)
	}
}
