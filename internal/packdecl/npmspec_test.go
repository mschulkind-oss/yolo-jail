package packdecl

// npmspec_test.go pins the SHAPE check on a `program via npm` selector.
//
// The field was validated for PRESENCE ONLY, and the next reader of the value is the
// in-jail launcher: internal/entrypoint's splitNpmSpec returns the version selector
// verbatim by design, so npmInstallSpec reconstructs `foo@@1.2.3` unchanged and hands it
// to `npm install -g` inside the container — the one place where diagnosing it is hardest
// and the author is least likely to be looking. The shape is knowable on the host, from
// the string alone, and that is all this check moves: the shapes npm itself refuses, and
// nothing stricter (no registry lookup, no version policy).
//
// TWO LEVELS, on purpose. The table exercises the predicate directly, and
// TestNpmSelectorShapeIsRefusedByTheManifestValidator goes through Decode — the CALL SITE
// the host actually reaches, and what `yolo check` picks up for free through the existing
// manifest load. A table over the predicate alone would stay green if the call site were
// deleted, which is the shape this repo has shipped five times.

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// npmSelectorCases is the accept/reject table. The accepted rows are every spelling npm
// resolves that a shipped pack or a plausible pack would write — a bare name, a scope
// (which is what four of the shipped packs use), an exact version, a dist-tag and a range
// — so a tightening that breaks one of them fails here rather than in a jail.
var npmSelectorCases = []struct {
	name string
	pkg  string
	want string // substring the problem must contain; "" = must be accepted
}{
	{"bare name", "opencode-ai", ""},
	{"scoped name", "@anthropic-ai/claude-code", ""},
	{"scoped name with exact version", "@openai/codex@1.2.3", ""},
	{"dist-tag", "opencode-ai@latest", ""},
	{"caret range", "opencode-ai@^1.0.0", ""},
	{"scoped caret range", "@github/copilot@^1.0.0", ""},
	// A trailing `@` is ACCEPTED here on purpose: splitNpmSpec already reads it as "no
	// version at all" and npmInstallSpec renders `foo@latest`, so the value never reaches
	// npm in the shape npm refuses. Refusing it would be stricter than the ruling.
	{"trailing at", "opencode-ai@", ""},
	{"empty is the required-field check's business", "", ""},

	{"doubled at", "foo@@1.2.3", "doubled"},
	{"doubled at before a scope", "@@acme/foo", "doubled"},
	{"space in the name", "foo bar", "whitespace"},
	{"space before the version", "foo @1.2.3", "whitespace"},
	{"tab", "foo\t1.2.3", "whitespace"},
	{"double quote", `foo"1.2.3`, "quote"},
	{"single quote", "foo'1.2.3", "quote"},
	{"backtick", "foo`whoami`", "quote"},
	{"newline", "foo\n1.2.3", "whitespace"},
}

// TestNpmPackageProblemShape sweeps the table over the predicate.
func TestNpmPackageProblemShape(t *testing.T) {
	for _, tc := range npmSelectorCases {
		t.Run(tc.name, func(t *testing.T) {
			got := NpmPackageProblem(tc.pkg)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("NpmPackageProblem(%q) = %q, want accepted — npm resolves this "+
					"spelling, so refusing it is stricter than the ruling", tc.pkg, got)
			case tc.want != "" && got == "":
				t.Errorf("NpmPackageProblem(%q) = accepted, want a problem mentioning %q — "+
					"npm refuses this spelling, and in-jail is the worst place to learn it",
					tc.pkg, tc.want)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("NpmPackageProblem(%q) = %q, want it to mention %q",
					tc.pkg, got, tc.want)
			}
		})
	}
}

// TestNpmSelectorShapeIsRefusedByTheManifestValidator is the CALL-SITE half, and the
// reason this file has two tests instead of one: the table above passes with the
// validation call deleted from validateContribution, which would leave the whole feature
// switched off with a green suite. This goes through Decode — the strict host decoder every
// authoring read and `yolo check`'s pack section reach through packload.LoadDir — so
// deleting the call makes it RED.
//
// Every rejected row from the table is driven through a real manifest, not just one
// representative, because the call site's job is to report whatever the predicate found:
// a call that ran only for some inputs would pass a one-row version of this test.
func TestNpmSelectorShapeIsRefusedByTheManifestValidator(t *testing.T) {
	for _, tc := range npmSelectorCases {
		if tc.want == "" || tc.pkg == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			// Encoded through the JSON path so control characters and quotes arrive in the
			// manifest exactly as an author would have written them.
			manifest := []byte(`{"name":"acme","contributes":[{"kind":"program","bin":"acme",` +
				`"via":"npm","package":` + jsonQuote(tc.pkg) + `}]}`)
			_, problems := Decode(manifest)
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("Decode accepted package %q — the shape check is not wired into "+
					"the manifest validator, so the selector reaches `npm install -g` in the "+
					"jail. problems:\n%s", tc.pkg, joined)
			}
			// The label has to name the field, or an author with three programs cannot tell
			// which one to fix.
			if !strings.Contains(joined, "contributes[0].package") {
				t.Errorf("the refusal does not name contributes[0].package:\n%s", joined)
			}
		})
	}
}

// TestNpmSelectorShapeAcceptsWhatTheShippedPacksDeclare is the other direction of the
// same call site: every npm selector a shipped pack declares uses the scoped or bare form,
// and a check that refused one of them would break every jail. Driven through Decode for
// the reason above.
//
// READ OFF THE TREE, not off a list. The list said "the four shipped packs that install via
// npm" and named them, which made it a second place to remember when a pack's `via` moves —
// and it moved on 2026-09-04, when codex went to its vendor's installer (OQ-PD13), leaving
// this cell asserting the validator against a package string no manifest declares any more.
// Reading the embedded manifests instead makes the cell track the packs by construction, and
// covers a pack added tomorrow for free.
func TestNpmSelectorShapeAcceptsWhatTheShippedPacksDeclare(t *testing.T) {
	entries, err := fs.ReadDir(officialpacks.FS, ".")
	if err != nil {
		t.Fatalf("reading the embedded packs: %v", err)
	}
	checked := 0
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(officialpacks.FS, path.Join(ent.Name(), ManifestName))
		if err != nil {
			continue // not every embedded pack ships a manifest at the root
		}
		m, problems := Decode(raw)
		if len(problems) != 0 {
			t.Errorf("Decode refused the shipped %s manifest: %v", ent.Name(), problems)
			continue
		}
		for _, inst := range m.InstallContributions() {
			if inst.Kind != "npm" {
				continue
			}
			checked++
			if inst.Package == "" {
				t.Errorf("%s declares a program via npm with no package", ent.Name())
			}
		}
	}
	// The floor: this cell has gone green while covering nothing before (an embed that stops
	// resolving reads as "no npm packs"), and it would do it silently.
	if checked == 0 {
		t.Error("no shipped pack declares a program via npm — either the last one flipped " +
			"(delete this test and say so) or the embed stopped resolving")
	}
}

// jsonQuote renders a Go string as a JSON string literal, so a test case carrying a
// newline or a quote can be embedded in a manifest without hand-escaping.
func jsonQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
