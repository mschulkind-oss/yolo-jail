package entrypoint

// tomltrivia_test.go pins E4's `rmw` half: a comment in a read-modify-written TOML file
// survives a render that changes an unrelated key, and the comments that do NOT survive are
// named rather than dropped in silence.
//
// The unit tests here work on the scanner and the re-attachment pass directly; the
// end-to-end proof over the shipped codex pack lives in hostrmwcodec_test.go, which is where
// the loss was reported before it was fixed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// tomlDecodeOrderedForTest decodes TOML into the order-preserving model the RMW writer uses.
func tomlDecodeOrderedForTest(src string) (*jsonx.OrderedMap, error) {
	return tomlx.DecodeOrdered([]byte(src))
}

// encodeTOMLForTest renders an object through the SAME canonical emitter the RMW writer
// uses, so a test exercises the real input to the re-attachment pass rather than a
// hand-written approximation of it.
func encodeTOMLForTest(t *testing.T, obj *jsonx.OrderedMap) string {
	t.Helper()
	c, _ := codec.LookupCodec("toml")
	encoded, err := c.Encode(tomlValue(obj))
	if err != nil {
		t.Fatalf("canonical toml encode: %v", err)
	}
	return string(encoded) + "\n"
}

// jsoncHostHome makes a temp home holding a commented ~/.copilot/config.json — copilot's
// `mode: rmw`, `codec: json` surface.
func jsoncHostHome(t *testing.T, content string) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".copilot", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

// renderCopilotConfigHost runs the shipped copilot pack at the host notch and returns the
// copilot/config result — a declared `rmw` surface with the `json` codec.
func renderCopilotConfigHost(t *testing.T, home string) HostRenderResult {
	t.Helper()
	copilot, err := embeddedPack("copilot")
	if err != nil {
		t.Fatalf("embedded copilot: %v", err)
	}
	results, rerr := RenderHostPack(copilot, home, false, nil)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	for _, r := range results {
		if r.Surface == "copilot/config" {
			return r
		}
	}
	t.Fatalf("no copilot/config result: %+v", results)
	return HostRenderResult{}
}

// readFileString reads a file or fails the test.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// scanOK scans TOML source and fails the test if the scanner bails out.
func scanOK(t *testing.T, src string) *tomlTrivia {
	t.Helper()
	tv, ok := scanTOMLTrivia([]byte(src))
	if !ok {
		t.Fatalf("scanTOMLTrivia refused valid TOML:\n%s", src)
	}
	return tv
}

// decodeOK decodes TOML into the RMW value model.
func decodeOK(t *testing.T, src string) *jsonx.OrderedMap {
	t.Helper()
	m, err := tomlDecodeOrderedForTest(src)
	if err != nil {
		t.Fatalf("decode %q: %v", src, err)
	}
	return m
}

// THE SCANNER places a comment in each of the five positions the acceptance list names.
func TestScanTOMLTriviaPositions(t *testing.T) {
	src := strings.Join([]string{
		"# a preamble about this file",
		"# spanning two lines",
		"",
		"# about model",
		`model = "gpt-5"  # the good one`,
		"",
		"# about the tui section",
		"[tui]",
		"# about theme",
		`theme = "dark"`,
		"",
		"# a parting thought",
		"",
	}, "\n")

	tv := scanOK(t, src)

	if len(tv.header) != 2 || !strings.Contains(tv.header[0], "preamble") {
		t.Errorf("header = %q, want the two-line preamble", tv.header)
	}
	if len(tv.footer) != 1 || !strings.Contains(tv.footer[0], "parting") {
		t.Errorf("footer = %q, want the parting thought", tv.footer)
	}
	if got := tv.leading["model"]; len(got) != 1 || !strings.Contains(got[0], "about model") {
		t.Errorf("leading[model] = %q", got)
	}
	if got := tv.inline["model"]; !strings.Contains(got, "the good one") {
		t.Errorf("inline[model] = %q", got)
	}
	if got := tv.leading["tui"]; len(got) != 1 || !strings.Contains(got[0], "tui section") {
		t.Errorf("leading[tui] = %q (a table header takes its own block)", got)
	}
	key := "tui" + tomlPathSep + "theme"
	if got := tv.leading[key]; len(got) != 1 || !strings.Contains(got[0], "about theme") {
		t.Errorf("leading[tui.theme] = %q (a key nested in a table)", got)
	}
	if tv.unplaced != 0 {
		t.Errorf("unplaced = %d, want 0 — every comment here has a home", tv.unplaced)
	}
}

// A `#` inside a STRING is not a comment. Misreading one would attach nonsense to the next
// key, which is worse than losing it.
func TestScanTOMLTriviaIgnoresHashesInStrings(t *testing.T) {
	src := strings.Join([]string{
		`url = "https://example.test/#frag"`,
		`lit = 'a#b'`,
		"ml = \"\"\"line\n#not a comment\n\"\"\"",
		"# real",
		"k = 1",
		"",
	}, "\n")

	tv := scanOK(t, src)
	if len(tv.header) != 0 || len(tv.footer) != 0 {
		t.Errorf("a hash inside a string was read as a comment: header=%q footer=%q",
			tv.header, tv.footer)
	}
	if got := tv.leading["k"]; len(got) != 1 || !strings.Contains(got[0], "real") {
		t.Errorf("leading[k] = %q, want the one real comment", got)
	}
	if len(tv.leading) != 1 || len(tv.inline) != 0 {
		t.Errorf("spurious attachments: leading=%v inline=%v", tv.leading, tv.inline)
	}
}

// A DETACHED mid-file block, a comment inside a multi-line value, and one under an
// [[array of tables]] are all counted as unplaceable rather than hoisted somewhere they did
// not come from.
func TestScanTOMLTriviaCountsUnplaceable(t *testing.T) {
	src := strings.Join([]string{
		"a = 1",
		"",
		"# floating, attached to nothing",
		"",
		"b = [",
		"  1,  # inside the value",
		"  2,",
		"]",
		"",
		"[[servers]]",
		"# under an array of tables",
		`name = "one"`,
		"",
	}, "\n")

	tv := scanOK(t, src)
	if tv.unplaced != 3 {
		t.Errorf("unplaced = %d, want 3 (detached + in-value + array-of-tables)", tv.unplaced)
	}
	if len(tv.leading) != 0 {
		t.Errorf("nothing here is placeable, got leading=%v", tv.leading)
	}
}

// A DOTTED key in the source addresses the same path the emitter writes as a table, so the
// comment follows the key across the layout change.
func TestScanTOMLTriviaDottedKey(t *testing.T) {
	tv := scanOK(t, "# about the theme\ntui.theme = \"dark\"\n")
	key := "tui" + tomlPathSep + "theme"
	if got := tv.leading[key]; len(got) != 1 {
		t.Errorf("leading[tui.theme] = %q, want the dotted key's own comment (have %v)",
			got, tv.leading)
	}
}

// THE HEADLINE CASE. A comment survives a render that changes an UNRELATED key.
func TestReattachTOMLCommentsSurvivesUnrelatedChange(t *testing.T) {
	src := strings.Join([]string{
		"# what this file is",
		"",
		"# the model I settled on after a week",
		`model = "gpt-5"  # not the mini`,
		"",
		"# terminal look",
		"[tui]",
		"# high contrast, my eyes are bad",
		`theme = "dark"`,
		"",
		"# end",
		"",
	}, "\n")

	before := decodeOK(t, src)
	after := decodeOK(t, src)
	after.Set("approval_policy", "on-request") // the unrelated key this render changes

	encoded := encodeTOMLForTest(t, after)
	text, losses := reattachTOMLComments(encoded, []byte(src), before, after)

	for _, want := range []string{
		"# what this file is",
		"# the model I settled on after a week",
		"# not the mini",
		"# terminal look",
		"# high contrast, my eyes are bad",
		"# end",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("comment %q did not survive:\n%s", want, text)
		}
	}
	if len(losses) != 0 {
		t.Errorf("nothing should be reported lost here: %v\n%s", losses, text)
	}
	// The result is still TOML, and the values are intact.
	rt := decodeOK(t, text)
	if v, _ := rt.Get("model"); v != "gpt-5" {
		t.Errorf("model = %#v after re-attachment", v)
	}
	if v, _ := rt.Get("approval_policy"); v != "on-request" {
		t.Errorf("approval_policy = %#v after re-attachment", v)
	}
	// The comment must sit ABOVE its key, not merely somewhere in the file — the whole
	// point of attachment over hoisting.
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "model =") {
			if i == 0 || !strings.Contains(lines[i-1], "settled on after a week") {
				t.Errorf("the comment is not directly above its key:\n%s", text)
			}
		}
	}
}

// RULE ①. A comment above a key whose VALUE this render changes is dropped — a comment
// explaining a value that is no longer there is worse than no comment — and the drop is
// REPORTED by key.
func TestReattachTOMLCommentsDropsOverriddenKey(t *testing.T) {
	src := strings.Join([]string{
		"# I want to be prompted for everything",
		`approval_policy = "untrusted"`,
		"# unrelated",
		`model = "gpt-5"`,
		"",
	}, "\n")

	before := decodeOK(t, src)
	after := decodeOK(t, src)
	after.Set("approval_policy", "on-request") // managed overrides it

	text, losses := reattachTOMLComments(encodeTOMLForTest(t, after), []byte(src), before, after)

	if strings.Contains(text, "prompted for everything") {
		t.Errorf("a comment above an OVERRIDDEN key must not survive to lie about it:\n%s", text)
	}
	if !strings.Contains(text, "# unrelated") {
		t.Errorf("the untouched key's comment must still survive:\n%s", text)
	}
	joined := strings.Join(losses, " ")
	if !strings.Contains(joined, "approval_policy") {
		t.Errorf("the drop must be REPORTED by key, got %v", losses)
	}
	if strings.Contains(joined, "`model`") {
		t.Errorf("an untouched key must not be reported as a loss: %v", losses)
	}
}

// A TABLE's own comment describes the SECTION, so yolo adding a key inside it does not
// falsify the comment and must not drop it. (The leaf rule and the table rule are different
// on purpose — see rmwTriviaKeeper.)
func TestReattachTOMLCommentsKeepsTableCommentWhenContentsChange(t *testing.T) {
	src := "# my terminal settings\n[tui]\ntheme = \"dark\"\n"
	before := decodeOK(t, src)
	after := decodeOK(t, src)
	tui, _ := after.Get("tui")
	tui.(*jsonx.OrderedMap).Set("notifications", true) // yolo adds a key under the table

	text, losses := reattachTOMLComments(encodeTOMLForTest(t, after), []byte(src), before, after)
	if !strings.Contains(text, "my terminal settings") {
		t.Errorf("a section comment must survive a change to the section's CONTENTS:\n%s", text)
	}
	if len(losses) != 0 {
		t.Errorf("nothing lost here, got %v", losses)
	}
}

// A comment whose KEY is gone from the render is dropped and reported as such — a distinct
// message from the rule-① case, because the remedy is different.
func TestReattachTOMLCommentsReportsVanishedKey(t *testing.T) {
	src := "# about the server\n[mcp_servers.mine]\ncommand = \"x\"\n"
	before := decodeOK(t, src)
	after := decodeOK(t, src)
	after.Delete("mcp_servers") // the wholesale table regeneration cleared it

	text, losses := reattachTOMLComments(encodeTOMLForTest(t, after), []byte(src), before, after)
	if strings.Contains(text, "about the server") {
		t.Errorf("a comment for a key that is gone must not be emitted:\n%s", text)
	}
	if len(losses) == 0 || !strings.Contains(strings.Join(losses, " "), "mcp_servers") {
		t.Errorf("the vanished key must be named: %v", losses)
	}
}

// FAIL-OPEN, not fail-wrong. A source the scanner cannot read falls back to the canonical
// bytes plus the blanket warning, because a MISPLACED comment is worse than a missing one.
func TestReattachTOMLCommentsFailsOpenOnUnscannableSource(t *testing.T) {
	// A `}` with no opening brace makes skipTOMLValue give up; the bytes still carry a
	// comment, so the blanket line must appear.
	src := "# a comment\nk = }\n"
	if _, ok := scanTOMLTrivia([]byte(src)); ok {
		t.Skip("the scanner now understands this source; pick another unscannable one")
	}
	after := jsonx.NewOrderedMap()
	after.Set("k", "v")
	text, losses := reattachTOMLComments(encodeTOMLForTest(t, after), []byte(src), nil, after)
	if strings.Contains(text, "# a comment") {
		t.Errorf("a source that could not be scanned must not have comments guessed into it:\n%s", text)
	}
	if len(losses) != 1 || !strings.Contains(losses[0], "NOT preserved") {
		t.Errorf("the fallback must warn: %v", losses)
	}
}

// An UNCOMMENTED file is untouched by the pass: the output is exactly the canonical emit,
// so the ordinary case cannot regress.
func TestReattachTOMLCommentsNoOpWithoutComments(t *testing.T) {
	src := "model = \"gpt-5\"\n\n[tui]\ntheme = \"dark\"\n"
	before := decodeOK(t, src)
	after := decodeOK(t, src)
	encoded := encodeTOMLForTest(t, after)
	text, losses := reattachTOMLComments(encoded, []byte(src), before, after)
	if text != encoded {
		t.Errorf("an uncommented file must round-trip the canonical bytes:\n%q\nvs\n%q",
			text, encoded)
	}
	if len(losses) != 0 {
		t.Errorf("no losses without comments: %v", losses)
	}
}

// A JSON surface has no comments to preserve, and the reason is structural rather than a
// policy choice: strict JSON has no comment syntax, so a commented file never decodes and
// the RMW path REFUSES it, untouched. E4 on a `json` surface is therefore vacuous, and this
// pins that it stays vacuous rather than becoming a silent loss.
func TestJSONSurfaceCommentsAreRefusedNotDropped(t *testing.T) {
	home, path := jsoncHostHome(t, "{\n  // why this is set\n  \"userKey\": 1\n}\n")
	got := renderCopilotConfigHost(t, home)
	if !strings.HasPrefix(got.Action, "refused:") {
		t.Fatalf("action = %q, want a refusal for a commented (i.e. invalid) JSON file", got.Action)
	}
	after := readFileString(t, path)
	if !strings.Contains(after, "why this is set") {
		t.Errorf("a refused file must be byte-untouched, comment included:\n%s", after)
	}
}
