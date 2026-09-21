package basehome

import "testing"

// fixtureDecls is the declaration set every classification case is read against. It is
// SPELLED OUT rather than derived from the shipped packs on purpose: the table then states
// what it assumes, and a pack manifest changing next month cannot silently rewrite what
// this test claims the rules do. decls_test.go pins the derive against the real packs
// separately — the two halves hostmigrate keeps apart for the same reason.
func fixtureDecls() Decls {
	return Decls{
		StateDirs:  []string{".claude", ".codex", ".copilot", ".gemini"},
		SharedDirs: []string{".claude-shared-credentials", ".gemini-shared-credentials"},
		CredentialFiles: []string{
			".claude/.credentials.json",
			".gemini/antigravity-cli/antigravity-oauth-token",
		},
		ConfigSurfaces: []string{
			".claude.json", // home-root, reached ONLY through the redirect
			".claude/settings.json",
			".codex/config.toml",
			".gemini/antigravity-cli/settings.json",
		},
		ContentDests: []string{
			".claude/CLAUDE.md",
			".claude/skills",
			".copilot/copilot-instructions.md",
		},
		Redirects: []Redirect{
			{Name: ".claude.json", Target: ".claude/claude.json"},
			{Name: ".gitconfig", Target: ".config/git/config"},
		},
	}
}

// TestClassifyAppliesTheOrderedRules is §5.2's rule list, one row per rule and per case
// the design or the recon named as load-bearing. Membership, never counts: the shipped
// declaration sets grow with every pack, so a row keyed on a path stays true where a
// length assertion goes red on an unrelated commit.
func TestClassifyAppliesTheOrderedRules(t *testing.T) {
	d := fixtureDecls()
	cases := []struct {
		name string
		rel  string
		want Class
		why  string
	}{
		// Rule 1 — under a declared machine-scope shared dir.
		{"shared dir itself", ".claude-shared-credentials", Credential,
			"the machine tier is excluded by class, permanently (§8)"},
		{"leaf under a shared dir", ".claude-shared-credentials/.credentials.json", Credential, ""},
		{"nested leaf under a shared dir", ".gemini-shared-credentials/a/b/token", Credential, ""},

		// Rule 2 — a declared credential path, and the core fallback.
		{"declared credential path", ".claude/.credentials.json", Credential,
			"declared by the claude pack's shared_credentials hook"},
		{"declared nested credential", ".gemini/antigravity-cli/antigravity-oauth-token", Credential, ""},
		{"core fallback carries codex", ".codex/auth.json", Credential,
			"NO pack declares a hook for codex; the fallback is what keeps its login"},
		{"core fallback anywhere", ".copilot/nested/oauth_creds.json", Credential, ""},
		{"declared basename that moved", ".codex/.credentials.json", Credential,
			"a credential one directory over is still a credential"},

		// Rule 3 — a config surface, joined through the redirects.
		{"leaf config surface", ".claude/settings.json", Config, ""},
		{"config surface in another pack", ".codex/config.toml", Config, ""},
		{"redirected home-root surface", ".claude/claude.json", Config,
			"~/.claude.json maps here; misclassify it and the oauthAccount file is archived"},
		{"unrelated file in a state dir", ".claude/settings.local.json", Runtime,
			"only the declared path is CONFIG, not its neighbours"},

		// Rule 4 — a declared content destination, leaf and subtree.
		{"briefing destination", ".claude/CLAUDE.md", Content, ""},
		{"skills destination itself", ".claude/skills", Content, ""},
		{"inside a skills destination", ".claude/skills/thing/SKILL.md", Content,
			"a directory destination owns its subtree"},

		// Rule 5 — everything else.
		{"transcript dir", ".claude/projects/-home-user-code-thing/x.jsonl", Runtime, ""},
		{"per_jail_history's own file", ".claude/history.jsonl", Runtime,
			"the OTHER hook kind: a POSITIVE runtime assertion, and the one entry a " +
				"missing hook-name filter would preserve while reporting success"},
		{"session state", ".copilot/session-state/events.jsonl", Runtime, ""},
		{"mixed dir's runtime leaf", ".gemini/antigravity-cli/debug.log", Runtime, ""},

		// Not home-relative: never a candidate, whoever asks.
		{"absolute path", "/etc/passwd", Credential,
			"not this walk's business; answered in the KEPT direction so it cannot be moved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := d.Classify(tc.rel); got != tc.want {
				t.Fatalf("Classify(%q) = %s, want %s. %s", tc.rel, got, tc.want, tc.why)
			}
		})
	}
}

// TestCandidateIsRuntimeOrUnclassified pins §5.1's candidate rule, which is the predicate
// the apply will consume. A class added later is NOT a candidate by default.
func TestCandidateIsRuntimeOrUnclassified(t *testing.T) {
	for _, c := range []Class{Credential, Config, Content} {
		if c.Candidate() {
			t.Fatalf("%s must never be a candidate — the whole point of the class is that it stays", c)
		}
	}
	for _, c := range []Class{Runtime, Unclassified} {
		if !c.Candidate() {
			t.Fatalf("%s must be a candidate (§5.1: RUNTIME, or cannot be classified at all)", c)
		}
	}
}

// TestZeroClassIsUnclassified pins the zero value. A zero value meaning Credential would
// make a forgotten assignment look like a successful keep; one meaning Runtime would make
// it look like an examined candidate.
func TestZeroClassIsUnclassified(t *testing.T) {
	var c Class
	if c != Unclassified {
		t.Fatalf("zero Class is %s, want UNCLASSIFIED", c)
	}
}

// TestSQLiteSidecarsFollowTheirDatabase pins §5.2's one-unit rule as a CLASSIFICATION
// fact, so it holds with no move to observe. The runtime direction is the one that
// matters in practice (all three move); the kept direction is what stops a half-move from
// corrupting a database some pack declared.
func TestSQLiteSidecarsFollowTheirDatabase(t *testing.T) {
	runtime := fixtureDecls()
	for _, p := range []string{
		".copilot/session-store.db", ".copilot/session-store.db-wal", ".copilot/session-store.db-shm",
	} {
		if got := runtime.Classify(p); got != Runtime {
			t.Fatalf("Classify(%q) = %s, want RUNTIME — the sibling set moves as one", p, got)
		}
	}

	kept := Decls{ConfigSurfaces: []string{".copilot/session-store.db"}}
	for _, p := range []string{
		".copilot/session-store.db", ".copilot/session-store.db-wal", ".copilot/session-store.db-shm",
	} {
		if kept.Classify(p).Candidate() {
			t.Fatalf("Classify(%q) is a candidate; moving a sidecar away from a KEPT database corrupts it", p)
		}
	}
	if got := kept.Classify(".copilot/other.db-wal"); got != Runtime {
		t.Fatalf("Classify(unrelated -wal) = %s, want RUNTIME — the rule follows the database, not the suffix", got)
	}
}

// TestHomeRelRejectsWhatIsNotHomeRelative pins the normalization every declaration goes
// through. An absolute surface path names no base-home entry, and coercing it would
// silently protect something unrelated.
func TestHomeRelRejectsWhatIsNotHomeRelative(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"~/.claude/settings.json", ".claude/settings.json", true},
		{".claude/CLAUDE.md", ".claude/CLAUDE.md", true},
		{"./.claude/skills/", ".claude/skills", true},
		{"/etc/hosts", "", false},
		{"~", "", false},
		{"~other/.claude", "", false},
		{"", "", false},
		{"../escape", "", false},
	}
	for _, tc := range cases {
		got, ok := HomeRel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("HomeRel(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
