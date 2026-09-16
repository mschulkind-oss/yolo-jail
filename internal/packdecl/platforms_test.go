package packdecl

import (
	"strings"
	"testing"
)

// platforms_test.go pins the `platforms` list as a PROGRAM declaration: WHERE THE VENDOR
// PUBLISHES A BUILD (setup-support-gaps.md §7 F8).
//
// The defect it closes was not a missing field but a field that decoded and went nowhere:
// `platforms` has lived on Contribution since the `service` kind landed, the struct is flat,
// so a `program` declaring it validated clean and InstallContributions dropped it —
// accepted-and-ignored, the shape this schema refuses everywhere else. Measured by CI:
// `install (ubuntu-24.04-arm, omp)` red on the vendor's own `oh-omp: unsupported platform
// linux-arm64` while ubuntu-latest was green.
//
// The GATE these cells describe lives at the consumer (internal/entrypoint's launcher
// generation); everything asserted here is schema-shaped.

// The list projects onto Install, and the predicate answers the three cases the grammar
// has: an exact pair, a bare GOOS (every architecture on it), and no declaration at all.
func TestProgramPlatformsProjectAndDecide(t *testing.T) {
	m, problems := Decode([]byte(`{
		"name": "omp-ish",
		"contributes": [
			{"kind": "program", "bin": "oh-omp", "via": "npm", "package": "oh-omp@1.0.0",
				"platforms": ["darwin/arm64", "linux/amd64"]},
			{"kind": "program", "bin": "oskind", "via": "installer",
				"url": "https://example.invalid/i.sh", "platforms": ["linux"]},
			{"kind": "program", "bin": "everywhere", "via": "npm", "package": "ubiq"}
		]
	}`))
	if len(problems) != 0 {
		t.Fatalf("a program declaring `platforms` must decode clean: %v", problems)
	}
	installs := m.InstallContributions()
	if len(installs) != 3 {
		t.Fatalf("InstallContributions() = %d entries, want 3", len(installs))
	}
	// The PROJECTION is the half that was missing: the declaration reached Contribution and
	// stopped there, so the jail's launcher generator — which reads Install and never the
	// manifest — could not see it.
	if got := strings.Join(installs[0].Platforms, ","); got != "darwin/arm64,linux/amd64" {
		t.Errorf("Install.Platforms = %q; the list must survive the projection or no "+
			"consumer can honor it", got)
	}
	// Carried for `installer` as well as `npm`: the vendor's platform set is a fact about
	// the program, not about how it arrives.
	if len(installs[1].Platforms) != 1 {
		t.Errorf("a `via installer` program lost its platforms: %+v", installs[1])
	}

	for _, tc := range []struct {
		inst         Install
		goos, goarch string
		want         bool
	}{
		{installs[0], "linux", "amd64", true},
		{installs[0], "darwin", "arm64", true},
		{installs[0], "linux", "arm64", false},  // the measured CI cell
		{installs[0], "darwin", "amd64", false}, // the pair is exact in both halves
		{installs[1], "linux", "arm64", true},   // a bare GOOS matches every arch on it
		{installs[1], "linux", "amd64", true},
		{installs[1], "darwin", "arm64", false},
		{installs[2], "linux", "arm64", true}, // absent means every platform
		{installs[2], "plan9", "386", true},
	} {
		if got := tc.inst.SupportsPlatform(tc.goos, tc.goarch); got != tc.want {
			t.Errorf("%s.SupportsPlatform(%q, %q) = %v, want %v (declared %v)",
				tc.inst.Bin, tc.goos, tc.goarch, got, tc.want, tc.inst.Platforms)
		}
	}
}

// PlatformsDeclared sorts for the message, and leaves the decoded field in the author's
// order — loopholedecl's same-named accessor has exactly this split, and a report that
// sorted in place would silently reorder what the manifest said.
func TestPlatformsDeclaredIsSortedAndTheFieldIsNot(t *testing.T) {
	in := Install{Bin: "x", Platforms: []string{"linux/amd64", "darwin/arm64"}}
	if got := strings.Join(in.PlatformsDeclared(), ", "); got != "darwin/arm64, linux/amd64" {
		t.Errorf("PlatformsDeclared() = %q, want the sorted rendering", got)
	}
	if in.Platforms[0] != "linux/amd64" {
		t.Errorf("Platforms[0] = %q; the decoded field keeps the author's order", in.Platforms[0])
	}
}

// An EMPTY list declares support for nothing, and is refused rather than read as
// "everywhere" — loopholedecl.parsePlatforms' argument, and the refusal carries both fixes.
func TestPlatformsEmptyListIsRefused(t *testing.T) {
	_, problems := Decode([]byte(
		`{"contributes":[{"kind":"program","bin":"b","via":"npm","package":"p","platforms":[]}]}`))
	if len(problems) == 0 {
		t.Fatal("an empty `platforms` list must be refused: honored literally it makes the " +
			"program uninstallable on every machine, and honored loosely it ignores what the " +
			"author wrote")
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "omit the key") {
		t.Errorf("the refusal must name the two fixes: %s", joined)
	}
	// `null` and an absent key are the same thing, which is what keeps every manifest
	// written before this key meaning what it meant.
	m, problems := Decode([]byte(
		`{"contributes":[{"kind":"program","bin":"b","via":"npm","package":"p","platforms":null}]}`))
	if len(problems) != 0 {
		t.Fatalf("`platforms: null` must read as absent: %v", problems)
	}
	if !m.InstallContributions()[0].SupportsPlatform("plan9", "386") {
		t.Error("an absent list must support every platform")
	}
}

// The two ways an ENTRY can be wrong without the closed GOOS/GOARCH vocabulary this package
// may not import: nothing at all, and a third segment.
func TestPlatformsEntryShapeIsChecked(t *testing.T) {
	for _, tc := range []struct{ entry, want string }{
		{`""`, "empty entry"},
		{`"   "`, "empty entry"},
		{`"linux/amd64/v3"`, "more than one"},
	} {
		_, problems := Decode([]byte(
			`{"contributes":[{"kind":"program","bin":"b","via":"npm","package":"p",` +
				`"platforms":[` + tc.entry + `]}]}`))
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, tc.want) {
			t.Errorf("platforms entry %s: problems = %q, want one naming %q",
				tc.entry, joined, tc.want)
		}
	}
	// And the vocabulary is deliberately NOT checked: `platforms: ["linux-x64"]` (the
	// vendor's own spelling) validates, because a second copy of loopholedecl's closed
	// GOOS/GOARCH list is the drift the field's doc refuses. What stops it being a silent
	// nothing is the consumer's line, which prints the declared set next to this machine's
	// platform. This cell exists so that gap is a recorded decision rather than an
	// oversight — delete it in the same change that adds the check.
	if _, problems := Decode([]byte(
		`{"contributes":[{"kind":"program","bin":"b","via":"npm","package":"p",` +
			`"platforms":["linux-x64"]}]}`)); len(problems) != 0 {
		t.Errorf("a GOOS spelling is not validated here (packdecl imports no vocabulary): %v",
			problems)
	}
}

// `platforms` is refused on every kind that reads it nowhere — `update`'s rule, in
// `update`'s position: a field accepted and ignored is a declaration that silently does
// nothing, which is precisely how F8 stayed invisible while `program` was one of them.
func TestPlatformsIsRefusedOnAKindThatReadsIt(t *testing.T) {
	for _, decl := range []string{
		`{"kind":"state","at":".omp","scope":"workspace","platforms":["linux"]}`,
		`{"kind":"requires","bin":"jq","platforms":["linux"]}`,
		`{"kind":"skills","into":".omp/skills","platforms":["linux"]}`,
	} {
		_, problems := Decode([]byte(`{"contributes":[` + decl + `]}`))
		joined := strings.Join(problems, "\n")
		if !strings.Contains(joined, "does not take \"platforms\"") {
			t.Errorf("%s: problems = %q, want the accepted-and-ignored refusal", decl, joined)
		}
	}
	// The two kinds that DO have an answer keep it.
	for _, decl := range []string{
		`{"kind":"program","bin":"b","via":"npm","package":"p","platforms":["linux/amd64"]}`,
		`{"kind":"service","name":"s","jail_daemon":{"cmd":["d"]},"platforms":["linux/amd64"]}`,
	} {
		if _, problems := Decode([]byte(`{"contributes":[` + decl + `]}`)); len(problems) != 0 {
			t.Errorf("%s: must validate clean, got %v", decl, problems)
		}
	}
}
