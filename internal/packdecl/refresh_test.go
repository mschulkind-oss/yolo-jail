package packdecl

import (
	"strings"
	"testing"
)

// refresh_test.go covers `program`'s `refresh` object: the PRE-LAUNCH REFRESH (a term coined
// for the field on 2026-09-25) that docs/design/pi-extension-lifecycle.md §3.2 calls the
// execution tier — an argv the launcher runs the installed program with before exec'ing it,
// under the lock §3.3 names.
//
// The properties worth pinning are the ones a silent failure would hide: the value SURVIVES
// the projection every consumer reads, it is refused on kinds no launcher serves, and each
// malformed shape that would do something other than it says is refused by name.

// TestRefreshSurvivesDecodeAndProjection is the load-bearing cell: InstallContributions is
// the ONLY way core reads a program, so a refresh that does not reach Install.Refresh never
// reaches a launcher however cleanly it decoded.
func TestRefreshSurvivesDecodeAndProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"@earendil-works/pi-coding-agent",
	   "refresh":{"argv":["update","--extensions"],"lock":".pi/.yolo-update.lock"}},
	  {"kind":"program","bin":"claude","via":"installer","url":"https://claude.ai/install.sh",
	   "refresh":{"argv":["plugins","sync"],"lock":".claude-shared/.yolo-lock"}},
	  {"kind":"program","bin":"copilot","via":"npm","package":"@github/copilot"}]}`))
	if len(probs) != 0 {
		t.Fatalf("a refresh should decode cleanly, got: %v", probs)
	}
	byBin := map[string]Install{}
	for _, in := range m.InstallContributions() {
		byBin[in.Bin] = in
	}
	pi := byBin["pi"].Refresh
	if pi == nil {
		t.Fatal("pi's refresh did not survive the projection")
	}
	if strings.Join(pi.Argv, " ") != "update --extensions" || pi.Lock != ".pi/.yolo-update.lock" {
		t.Errorf("pi's refresh arrived altered: %+v", *pi)
	}
	// BOTH vias: the refresh is what the program does to its add-ons, not how it arrived.
	if byBin["claude"].Refresh == nil {
		t.Error("an installer-delivered program's refresh must survive the projection too")
	}
	// Absence stays absence — a defaulted refresh would run something nobody declared.
	if byBin["copilot"].Refresh != nil {
		t.Errorf("a program declaring no refresh must project none, got %+v", *byBin["copilot"].Refresh)
	}

	// The projection COPIES: a consumer editing its Install must not reach the manifest.
	pi.Argv[0] = "mutated"
	again := map[string]Install{}
	for _, in := range m.InstallContributions() {
		again[in.Bin] = in
	}
	if again["pi"].Refresh.Argv[0] != "update" {
		t.Error("InstallContributions aliased the manifest's refresh argv")
	}
}

// TestRefreshIsRefusedOnKindsWithNoLauncher: `requires` installs nothing and the content kinds
// have no program, so a refresh on any of them is read by no consumer.
func TestRefreshIsRefusedOnKindsWithNoLauncher(t *testing.T) {
	for _, entry := range []string{
		`{"kind":"requires","bin":"fzf","refresh":{"argv":["x"],"lock":"a/b"}}`,
		`{"kind":"skills","into":".claude/skills","refresh":{"argv":["x"],"lock":"a/b"}}`,
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[` + entry + `]}`))
		if !strings.Contains(strings.Join(probs, "\n"), `does not take "refresh"`) {
			t.Errorf("%s should be refused by name, got: %v", entry, probs)
		}
	}
}

// TestRefreshRefusesMalformedShapes: each case is a declaration that would otherwise run
// something other than it says, with no message.
func TestRefreshRefusesMalformedShapes(t *testing.T) {
	for _, tc := range []struct {
		name, refresh, want string
	}{
		{"no argv", `{"lock":"s/.yolo-l"}`, "refresh.argv: required"},
		{"empty argv", `{"argv":[],"lock":"s/.yolo-l"}`, "refresh.argv: required"},
		{"empty word", `{"argv":["update","  "],"lock":"s/.yolo-l"}`, "refresh.argv[1]: empty word"},
		{"no lock", `{"argv":["update"]}`, "refresh.lock: required"},
		{"absolute lock", `{"argv":["update"],"lock":"/tmp/l"}`, "must be relative"},
		{"escaping lock", `{"argv":["update"],"lock":"s/../../l"}`, `must not contain ".."`},
		{"lock with no store", `{"argv":["update"],"lock":".yolo-update.lock"}`, "guards nothing"},
		{"unclean lock", `{"argv":["update"],"lock":"./s//.yolo-l/"}`, "must be a clean"},
		// The lock lives INSIDE the store, so its name must mark it as yolo's bookkeeping, or
		// a store holding nothing but the lock reads as populated to the shared_directory
		// hook, which then discards a workspace's real tree (sharedTreeIsEmpty).
		{"unmarked lock name", `{"argv":["update"],"lock":".pi/update.lock"}`, "must be named .yolo-"},
		{"marked store, unmarked lock", `{"argv":["update"],"lock":".yolo-store/lock"}`, "must be named .yolo-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"x","contributes":[
			  {"kind":"program","bin":"t","via":"npm","package":"t","refresh":` + tc.refresh + `}]}`))
			joined := strings.Join(probs, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got: %v", tc.want, probs)
			}
		})
	}
}

// TestRefreshSurvivesTheTolerantDecoder: the jail reads staged manifests through
// DecodeTolerant, and a refresh that only the strict path carried would validate for its
// author and never reach the launcher.
func TestRefreshSurvivesTheTolerantDecoder(t *testing.T) {
	m, probs, skipped := DecodeTolerant([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"p",
	   "refresh":{"argv":["update","--extensions"],"lock":"s/.yolo-l"}}]}`))
	if len(probs) != 0 || len(skipped) != 0 {
		t.Fatalf("problems=%v skipped=%v", probs, skipped)
	}
	installs := m.InstallContributions()
	if len(installs) != 1 || installs[0].Refresh == nil || installs[0].Refresh.Lock != "s/.yolo-l" {
		t.Fatalf("the tolerant path dropped the refresh: %+v", installs)
	}
}

// TestRefreshDueOnChangeDecodesAndProjects: the watched files survive the strict and the
// tolerant decoder and InstallContributions — the only road to the launcher — as a copy.
func TestRefreshDueOnChangeDecodesAndProjects(t *testing.T) {
	doc := []byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"p",
	   "refresh":{"argv":["update"],"lock":"s/.yolo-l","due_on_change":[".pi/agent/settings.json"]}}]}`)
	m, probs := Decode(doc)
	if len(probs) != 0 {
		t.Fatalf("problems: %v", probs)
	}
	got := m.InstallContributions()[0].Refresh.DueOnChange
	if strings.Join(got, ",") != ".pi/agent/settings.json" {
		t.Fatalf("the strict path lost due_on_change: %v", got)
	}
	got[0] = "mutated"
	if again := m.InstallContributions()[0].Refresh.DueOnChange[0]; again != ".pi/agent/settings.json" {
		t.Errorf("the projection must copy the list, not alias it: %q", again)
	}
	tm, tprobs, skipped := DecodeTolerant(doc)
	if len(tprobs) != 0 || len(skipped) != 0 {
		t.Fatalf("tolerant: problems=%v skipped=%v", tprobs, skipped)
	}
	if got := tm.InstallContributions()[0].Refresh.DueOnChange; len(got) != 1 {
		t.Errorf("the tolerant path lost due_on_change: %v", got)
	}
	// Absent means a stamp-only refresh: no list, not an empty one.
	m2, _ := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"t","via":"npm","package":"t","refresh":{"argv":["u"],"lock":"s/.yolo-l"}}]}`))
	if m2.InstallContributions()[0].Refresh.DueOnChange != nil {
		t.Errorf("an undeclared due_on_change must project as nil")
	}
}

// TestRefreshDueOnChangeRefusesMalformedLists: each is a trigger that could never fire, or
// would hash something outside the home the launcher is scoped to.
func TestRefreshDueOnChangeRefusesMalformedLists(t *testing.T) {
	for _, tc := range []struct{ name, due, want string }{
		{"not a list", `".pi/agent/settings.json"`, "due_on_change"},
		{"empty list", `[]`, "watches nothing"},
		{"empty entry", `[""]`, "empty path"},
		{"absolute", `["/etc/passwd"]`, "must be relative"},
		{"escaping", `["a/../../x"]`, `must not contain ".."`},
		{"unclean", `["./a//b"]`, "clean home-relative FILE"},
		{"a directory", `["a/b/"]`, "clean home-relative FILE"},
		{"duplicate", `["a","a"]`, "listed twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"x","contributes":[
			  {"kind":"program","bin":"t","via":"npm","package":"t",
			   "refresh":{"argv":["u"],"lock":"s/.yolo-l","due_on_change":` + tc.due + `}}]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("want a problem containing %q, got: %v", tc.want, probs)
			}
		})
	}
}

// TestRefreshOnlyIfRefusesMalformedTests: each is a worth-running test (XB-D23) that could never
// answer yes, or one the launcher's one-line `case` match cannot read as written. A good one
// decodes cleanly, so the cells refuse for their own reason.
func TestRefreshOnlyIfRefusesMalformedTests(t *testing.T) {
	decode := func(onlyIf string) []string {
		_, probs := Decode([]byte(`{"name":"x","contributes":[
		  {"kind":"program","bin":"t","via":"npm","package":"t",
		   "refresh":{"argv":["u"],"lock":"s/.yolo-l","only_if":` + onlyIf + `}}]}`))
		return probs
	}
	if probs := decode(`{"files":[".t/settings.json"],"project_files":[".t/s.json"],"contains":["\"npm:"]}`); len(probs) != 0 {
		t.Fatalf("a good only_if is refused: %v", probs)
	}
	for _, tc := range []struct{ name, onlyIf, want string }{
		{"no file", `{"contains":["\"npm:"]}`, "only_if: names no file"},
		{"empty file lists", `{"files":[],"project_files":[],"contains":["\"npm:"]}`, "only_if: names no file"},
		{"an escaping file", `{"files":["a/../../x"],"contains":["\"npm:"]}`, `must not contain ".."`},
		{"an absolute project file", `{"project_files":["/etc/x"],"contains":["\"npm:"]}`, "must be relative"},
		{"no contains", `{"files":[".t/s.json"]}`, "only_if.contains: required"},
		{"empty contains", `{"files":[".t/s.json"],"contains":[]}`, "only_if.contains: required"},
		{"an empty string", `{"files":[".t/s.json"],"contains":["\"npm:",""]}`, "only_if.contains[1]: must be a non-empty string on one line"},
		{"a multi-line string", `{"files":[".t/s.json"],"contains":["a\nb"]}`, "only_if.contains[0]: must be a non-empty string on one line"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if probs := decode(tc.onlyIf); !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("want a problem containing %q, got: %v", tc.want, probs)
			}
		})
	}
}

// TestProbeRefusesMalformedArguments: probe arguments (XB-D24, `probe_args`) are a program's alone, since only
// a launcher reads them, and each is one word, since the launcher compares it to its first
// argument whole. A good list decodes cleanly and survives the projection.
func TestProbeRefusesMalformedArguments(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"t","via":"npm","package":"t","probe_args":["--version","-v"]}]}`))
	if len(probs) != 0 {
		t.Fatalf("a good probe is refused: %v", probs)
	}
	if got := m.InstallContributions()[0].ProbeArgs; strings.Join(got, " ") != "--version -v" {
		t.Errorf("the probe did not survive the projection: %q", got)
	}
	for _, tc := range []struct{ name, entry, want string }{
		{"on a non-program", `{"kind":"requires","bin":"fzf","probe_args":["--version"]}`, `does not take "probe_args"`},
		{"an empty list", `{"kind":"program","bin":"t","via":"npm","package":"t","probe_args":[]}`, "probe_args: an empty list makes nothing a version probe"},
		{"an empty argument", `{"kind":"program","bin":"t","via":"npm","package":"t","probe_args":["--version",""]}`, "probe_args[1]"},
		{"a spaced argument", `{"kind":"program","bin":"t","via":"npm","package":"t","probe_args":["--ver sion"]}`, "probe_args[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, probs := Decode([]byte(`{"name":"x","contributes":[` + tc.entry + `]}`))
			if !strings.Contains(strings.Join(probs, "\n"), tc.want) {
				t.Errorf("want a problem containing %q, got: %v", tc.want, probs)
			}
		})
	}
}
