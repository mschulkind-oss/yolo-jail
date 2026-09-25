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
	   "refresh":{"argv":["update","--extensions"],"lock":".pi-shared-npm/.yolo-update.lock"}},
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
	if strings.Join(pi.Argv, " ") != "update --extensions" || pi.Lock != ".pi-shared-npm/.yolo-update.lock" {
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
		{"unmarked lock name", `{"argv":["update"],"lock":".pi-shared-npm/update.lock"}`, "must be named .yolo-"},
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
