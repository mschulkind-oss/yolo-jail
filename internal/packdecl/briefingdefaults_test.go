package packdecl

// briefingdefaults_test.go pins the schema half of the briefing defaults (docs/reference/pack-system.md#briefing): the rules a
// manifest is refused or accepted by, through Decode — the door a pack author knocks on —
// rather than through validateContributions, so a check that is written but never wired into
// the strict path fails here.
//
//   - P2: a briefing/skills contribution naming neither `into` nor `agents` is a BROADCAST, and
//     validates. `files` cannot broadcast, and its refusal says why.
//   - P5: a DESTINATION (`agent` set) sources nothing, so `from` on one is refused on every
//     kind, naming the addressed spelling. `agent` without `into` stays refused.
//   - OQ-PB2: a briefing source whose basename is AGENTS.md, CLAUDE.md or GEMINI.md is refused
//     at any depth, naming the move — and a DESTINATION or a host file of that name is not.
//   - OQ-PB5: two content contributions of one kind naming one source are refused, naming both.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P2: SILENCE IS A BROADCAST, in a manifest too. Every row here was refused with
// `needs "into"` before, which left "list every agent" as the only spelling of "every agent" —
// and that spelling is fatal in any jail not selecting one of the named agents.
func TestContentWithNoRouteIsABroadcast(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing"}`,
		`{"kind":"skills"}`,
		`{"kind":"briefing","from":"briefing/house-rules.md"}`,
		`{"kind":"briefing","from":"prose/all.md"}`,
		`{"kind":"skills","from":"my-skills"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s names no route and must validate as a broadcast, got %q", raw, probs)
		}
	}
}

// `files` DOES NOT BROADCAST (pack-system.md#briefing-non-goals), and the refusal names the reason and the addressed
// spelling rather than only the missing field.
func TestFilesWithNoRouteIsRefusedWithTheReason(t *testing.T) {
	probs := decodeOne(t, `{"kind":"files","from":"prompts"}`)
	for _, want := range []string{
		`kind "files" needs "into" or "agents"`,
		"cannot broadcast",
		"slot types",
		`{"kind":"files","agents":["<agent>"],"from":"prompts"}`,
	} {
		if !strings.Contains(probs, want) {
			t.Errorf("a route-less files contribution must be refused naming %q, got %q", want, probs)
		}
	}
	// The addressed and the pathed spellings of the same tree still validate.
	for _, raw := range []string{
		`{"kind":"files","from":"prompts","agents":["pi"]}`,
		`{"kind":"files","from":"prompts","into":".acme/prompts"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s must validate, got %q", raw, probs)
		}
	}
}

// The P2 relaxation is for CONTENT only: a destination is its path, so `agent` without `into`
// stays refused on every kind.
func TestDestinationStillNeedsInto(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"briefing","agent":"claude"}`,
		`{"kind":"skills","agent":"claude"}`,
		`{"kind":"files","agent":"pi"}`,
	} {
		if probs := decodeOne(t, raw); !strings.Contains(probs, `needs "into"`) {
			t.Errorf("%s declares a destination with no path and must be refused, got %q", raw, probs)
		}
	}
}

// P5: A DESTINATION SHIPS NOTHING. `files` already refused `from` on one; briefing and skills
// now do too, with the same message shape naming the addressed spelling that ships the pack's
// own content — the thing an agent pack wanting prose for its own agent writes instead.
func TestFromOnADestinationIsRefusedNamingTheAddressedSpelling(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md","from":"briefing/c.md"}`,
			`{"kind":"briefing","agents":["claude"],"from":"briefing/c.md"}`},
		{`{"kind":"skills","agent":"claude","into":".claude/skills","from":"skills"}`,
			`{"kind":"skills","agents":["claude"],"from":"skills"}`},
		{`{"kind":"files","agent":"pi","into":".pi/agent/extensions","from":"extensions"}`,
			`{"kind":"files","agents":["pi"],"from":"extensions"}`},
	} {
		probs := decodeOne(t, tc.raw)
		if !strings.Contains(probs, `takes no "from"`) || !strings.Contains(probs, tc.want) {
			t.Errorf("%s must be refused naming %s, got %q", tc.raw, tc.want, probs)
		}
	}
	// Every shipped agent pack's line — a bare destination — is untouched.
	for _, raw := range []string{
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"}`,
		`{"kind":"skills","agent":"claude","into":".claude/skills"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("a bare destination %s must validate, got %q", raw, probs)
		}
	}
}

// OQ-PB2: A REPOSITORY INSTRUCTION FILE IS NEVER A BRIEFING SOURCE, at any depth and however the
// path is spelled. The refusal names the move, because the fix is a `git mv`, not a flag.
func TestRepositoryInstructionFileIsRefusedAsABriefingSource(t *testing.T) {
	for _, from := range []string{
		"AGENTS.md", "./AGENTS.md", "x/CLAUDE.md", "GEMINI.md", "notes/deep/AGENTS.md",
	} {
		probs := decodeOne(t, `{"kind":"briefing","from":"`+from+`","agents":["claude"]}`)
		for _, want := range []string{
			"repository's own agent instructions",
			"git mv",
			`"from": "briefing/prose.md"`,
			`drop "from"`,
		} {
			if !strings.Contains(probs, want) {
				t.Errorf("from %q must be refused naming the move (%q), got %q", from, want, probs)
			}
		}
	}
	// Inside briefing/ the remedy is a RENAME, and it says so rather than "move under briefing/".
	probs := decodeOne(t, `{"kind":"briefing","from":"briefing/AGENTS.md"}`)
	if !strings.Contains(probs, "Rename it inside briefing/") ||
		!strings.Contains(probs, "git mv briefing/AGENTS.md briefing/prose.md") {
		t.Errorf("briefing/AGENTS.md must be refused naming the rename, got %q", probs)
	}

	// SOURCES only, and exact case. A DESTINATION ending in AGENTS.md is where five shipped
	// agents read; `after: "host:…"` names a host file; neither is a source. `agents.md` is not
	// the name any tool matches.
	for _, raw := range []string{
		`{"kind":"briefing","agent":"pi","into":".pi/agent/AGENTS.md"}`,
		`{"kind":"briefing","into":".config/opencode/AGENTS.md","after":"host:AGENTS.md"}`,
		`{"kind":"briefing","from":"briefing/agents.md"}`,
		`{"kind":"briefing","from":"prose/Claude.md"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s names no repository instruction file as a source and must validate, got %q",
				raw, probs)
		}
	}
}

// The reserved-name refusal is PER ENTRY, so the tolerant in-jail read refuses it too: the
// readers may rely on no declared `from` ever naming one, rather than each re-asking.
func TestRepositoryInstructionFileRefusalSurvivesTheTolerantDecode(t *testing.T) {
	_, probs, _ := DecodeTolerant([]byte(`{"name":"acme","contributes":[
	  {"kind":"briefing","from":"AGENTS.md"}]}`))
	if !strings.Contains(strings.Join(probs, "; "), "repository's own agent instructions") {
		t.Errorf("DecodeTolerant must refuse a reserved briefing source, got %v", probs)
	}
}

func TestRepositoryInstructionFilePredicate(t *testing.T) {
	for rel, want := range map[string]bool{
		"AGENTS.md":           true,
		"./CLAUDE.md":         true,
		"a/b/GEMINI.md":       true,
		"briefing/AGENTS.md":  true,
		"agents.md":           false,
		"AGENTS.md.bak":       false,
		"briefing/rules.md":   false,
		"AGENTS.md/notes.txt": false,
		"":                    false,
	} {
		if got := RepositoryInstructionFile(rel); got != want {
			t.Errorf("RepositoryInstructionFile(%q) = %v, want %v", rel, got, want)
		}
	}
	names := RepositoryInstructionFileNames()
	if strings.Join(names, ",") != "AGENTS.md,CLAUDE.md,GEMINI.md" {
		t.Errorf("RepositoryInstructionFileNames() = %v", names)
	}
	names[0] = "mutated"
	if RepositoryInstructionFileNames()[0] != "AGENTS.md" {
		t.Error("RepositoryInstructionFileNames must return a fresh slice")
	}
}

// OQ-PB1's shape: one level inside briefing/, *.md, exact case.
func TestConventionalBriefingFilePredicate(t *testing.T) {
	for rel, want := range map[string]bool{
		"briefing/house-rules.md":   true,
		"./briefing/a.md":           true,
		"briefing/AGENTS.md":        true, // the SHAPE; packload.LoadDir refuses the name
		"briefing/sub/a.md":         false,
		"briefing/a.txt":            false,
		"briefing/a.MD":             false,
		"Briefing/a.md":             false,
		"briefing":                  false,
		"a.md":                      false,
		"prose/briefing/a.md":       false,
		"briefing/../briefing/x.md": true,
		"":                          false,
	} {
		if got := ConventionalBriefingFile(rel); got != want {
			t.Errorf("ConventionalBriefingFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

// OQ-PB5: TWO CONTRIBUTIONS NAMING ONE SOURCE ARE REFUSED, naming both indices. The source is
// the CLEANED `from` (R4), an omitted briefing `from` is the convention's remainder, and an
// omitted skills `from` is "skills" — the tree is one unit.
func TestDuplicateContentSourcesAreRefused(t *testing.T) {
	// example is the merged spelling the refusal offers: it keeps a declared non-convention
	// `from`, because following an example that dropped it would move the contribution onto the
	// convention and stop the named file being delivered.
	for _, tc := range []struct{ name, contributes, want, example string }{
		{"two broadcasts of the convention", `{"kind":"briefing","into":".claude/CLAUDE.md"},
		  {"kind":"briefing","into":".pi/agent/AGENTS.md"}`,
			`contributes[1]: a second "briefing" contribution naming every briefing/*.md no other ` +
				`contribution names (both omit "from") (first at contributes[0])`,
			`{"kind":"briefing","agents":["claude","pi"]}`},
		{"one file spelled two ways", `{"kind":"briefing","from":"a.md","agents":["claude"]},
		  {"kind":"env","vars":{"A":"1"}},
		  {"kind":"briefing","from":"./a.md","agents":["pi"]}`,
			`contributes[2]: a second "briefing" contribution naming the source "a.md" (first at contributes[0])`,
			`{"kind":"briefing","from":"a.md","agents":["claude","pi"]}`},
		{"skills omitted and named", `{"kind":"skills","agents":["claude"]},
		  {"kind":"skills","from":"skills/","agents":["pi"]}`,
			`contributes[1]: a second "skills" contribution naming the source "skills" (first at contributes[0])`,
			`{"kind":"skills","agents":["claude","pi"]}`},
		{"a named skills tree", `{"kind":"skills","from":"pi-skills","agents":["claude"]},
		  {"kind":"skills","from":"pi-skills","agents":["pi"]}`,
			`contributes[1]: a second "skills" contribution naming the source "pi-skills" (first at contributes[0])`,
			`{"kind":"skills","from":"pi-skills","agents":["claude","pi"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"acme","contributes":[` + tc.contributes + `]}`))
			joined := strings.Join(probs, "; ")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got %q", tc.want, joined)
			}
			// The remedy is spelled: one contribution with every audience, or silence.
			if !strings.Contains(joined, tc.example) || !strings.Contains(joined, `omit both "into" and "agents"`) {
				t.Errorf("the refusal must name the one-contribution spellings (%s), got %q", tc.example, joined)
			}
		})
	}
}

// What OQ-PB5 does NOT refuse: different sources, the same source across kinds, and a
// DESTINATION beside the pack's own content — which is exactly how an agent pack addresses
// prose to itself (pack-system.md#briefing-p5). Two destinations are not content either.
func TestDistinctSourcesAndDestinationsAreNotDuplicates(t *testing.T) {
	for _, contributes := range []string{
		`{"kind":"briefing","from":"briefing/a.md","agents":["pi"]},{"kind":"briefing"}`,
		`{"kind":"briefing","from":"briefing/a.md"},{"kind":"briefing","from":"briefing/b.md"}`,
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"},{"kind":"briefing","agents":["claude"]}`,
		`{"kind":"skills","agent":"claude","into":".claude/skills"},{"kind":"skills"}`,
		`{"kind":"briefing","agent":"claude","into":".claude/CLAUDE.md"},{"kind":"briefing","agent":"claw","into":".claw/CLAW.md"}`,
		`{"kind":"skills","from":"x"},{"kind":"files","from":"x","agents":["pi"]}`,
	} {
		if _, probs := Decode([]byte(`{"name":"acme","contributes":[` + contributes + `]}`)); len(probs) != 0 {
			t.Errorf("%s must validate, got %v", contributes, probs)
		}
	}
}

// Strict path only, like every sibling check: the tolerant decode cannot see siblings and treats
// a problem as a fatal boot, so a duplicate must not acquire a new way to refuse a jail there.
// The readers keep a dedup for that read.
func TestDuplicateContentSourcesAreStrictOnly(t *testing.T) {
	_, probs, _ := DecodeTolerant([]byte(`{"name":"acme","contributes":[
	  {"kind":"briefing","agents":["claude"]},{"kind":"briefing","agents":["pi"]}]}`))
	if len(probs) != 0 {
		t.Errorf("DecodeTolerant must not run the sibling check, got %v", probs)
	}
}

// Every pack yolo SHIPS still decodes clean under the new refusals. The embedded packs are staged
// into every jail and read at every launch, so a shipped manifest that trips one is a refused
// launch for everyone selecting it.
func TestShippedManifestsSurviveTheBriefingDefaultsRefusals(t *testing.T) {
	manifests, err := filepath.Glob(filepath.Join(packsRepoRoot(t), "packs", "*", ManifestName))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no shipped manifests found (%v)", err)
	}
	for _, p := range manifests {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, probs := Decode(data); len(probs) != 0 {
			t.Errorf("%s: %v", p, probs)
		}
	}
}
