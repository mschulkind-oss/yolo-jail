package run

// sealdisclosure_test.go pins the read disclosure of a SEALED launch (notePackHostAccess under
// Options.Sealed; seal.go's sealKeepsClaim and sealedWithheldLine): it lists only what the build
// itself fetches or runs — a fork's source build, a patched extension's tree, an installer — and
// counts what the seal withheld in one line naming the packs that declared it, since a disclosure of
// a read that does not happen is worse than silence (DP-B2). The end-to-end half, on a real sealed
// launch, is TestASealedBuildIsHandedNothingItsBasesNeedsDeclare.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// sealDisclosurePacks is a build jail's selection: a fork and a patched extension, whose claims
// are what the build runs, beside a base declaring an env var, a host read and a host briefing,
// and a pack declaring a credential pointer, each of which the seal withholds.
func sealDisclosurePacks(t *testing.T) []*packload.Pack {
	t.Helper()
	return []*packload.Pack{
		{Name: "pi-fork", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindProgram, Bin: "pi", Via: packdecl.ViaSource, ForkOf: "pi",
			Source: "git+https://example.invalid/pi-fork?ref=main", Build: "make install",
			Produces: []string{".local/bin/pi"},
		}}}},
		{Name: "pi-ext", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
			Kind: packdecl.KindFiles, Into: ".pi/agent/extensions/sealtest/",
			Source: "git+https://example.invalid/sealtest-ext?ref=main", Patches: "patches",
			Follow: "branch:main", Build: "npm ci",
		}}}},
		{Name: "pi", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindEnv, Vars: map[string]string{"SEALTEST_PI_FLAG": "1"}},
			{Kind: packdecl.KindReadsHost, Host: ".pi/agent/settings.json"},
			{Kind: packdecl.KindBriefing, Into: ".pi/agent/AGENTS.md", After: "host:.pi/agent/AGENTS.md"},
		}}},
		{Name: "creds", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindEnv, Vars: map[string]string{"SEALTEST_TOKEN": "{caller_token}"}},
		}}},
	}
}

// sealDisclosure is what notePackHostAccess prints for packs, sealed or not.
func sealDisclosure(t *testing.T, sealed bool, packs []*packload.Pack) string {
	t.Helper()
	var stderr bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = discardBuf(), &stderr
	o.Sealed = sealed
	o.notePackHostAccess(packs, nil, nil)
	return stderr.String()
}

func TestASealedLaunchDisclosesOnlyWhatItsBuildRuns(t *testing.T) {
	packs := sealDisclosurePacks(t)
	got := sealDisclosure(t, true, packs)

	// The build's own claims: the fork's source build and the patched extension's tree.
	for _, want := range []string{"Pack environment this launch:\n",
		"  pi-fork: RUNS a program built from source, INSIDE THE JAIL",
		"  pi-ext: DELIVERS a tree built from source at ~/.pi/agent/extensions/sealtest"} {
		if !strings.Contains(got, want) {
			t.Errorf("a sealed launch's disclosure lacks %q, a claim about what its build runs:\n%s", want, got)
		}
	}
	// Nothing the seal withholds is listed.
	for _, never := range []string{"SEALTEST_PI_FLAG", "settings.json", "AGENTS.md", "{caller_token}", "  pi:", "  creds:"} {
		if strings.Contains(got, never) {
			t.Errorf("a sealed launch discloses %q, which the seal withholds:\n%s", never, got)
		}
	}
	// What was withheld is said once, counted, with the packs that declared it.
	const withheld = "Sealed build: 2 pack env vars and 2 host reads declared by pi, creds are withheld " +
		"(FP-D9: a build jail gets no credential and no host file)\n"
	if !strings.HasSuffix(got, withheld) {
		t.Errorf("a sealed launch's disclosure does not end in the withheld line:\n--- got ---\n%s--- want suffix ---\n%s", got, withheld)
	}
}

// The control: unsealed, the same selection lists every claim and prints no withheld line, so the
// sealed test's silence is the seal's.
func TestAnUnsealedLaunchDisclosesEveryReadClaim(t *testing.T) {
	got := sealDisclosure(t, false, sealDisclosurePacks(t))
	for _, want := range []string{"pi-fork: RUNS a program built from source", "pi-ext: DELIVERS a tree",
		"SEALTEST_PI_FLAG=1", "~/.pi/agent/settings.json", "~/.pi/agent/AGENTS.md", "SEALTEST_TOKEN={caller_token}"} {
		if !strings.Contains(got, want) {
			t.Errorf("an unsealed launch's disclosure lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Sealed build:") {
		t.Errorf("an unsealed launch prints the seal's withheld line:\n%s", got)
	}
}

// A sealed launch whose packs declare only what the build runs prints no withheld line, and one
// whose packs declare only withheld claims prints the line alone, with no empty block header.
func TestASealedLaunchPrintsEachHalfOnlyWhenItHasALine(t *testing.T) {
	packs := sealDisclosurePacks(t)
	if got := sealDisclosure(t, true, packs[:2]); strings.Contains(got, "Sealed build:") {
		t.Errorf("a sealed launch withholding nothing prints a withheld line:\n%s", got)
	}
	got := sealDisclosure(t, true, packs[3:])
	const want = "Sealed build: 1 pack env var declared by creds is withheld " +
		"(FP-D9: a build jail gets no credential and no host file)\n"
	if got != want {
		t.Errorf("a sealed launch withholding one env var:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

// sealedWithheldLine's count phrases, singular and plural, for every kind it counts.
func TestTheSealedWithheldLineCountsEachKind(t *testing.T) {
	line := func(kinds ...packdecl.Kind) string {
		var ls []disclosureLine
		for i, k := range kinds {
			ls = append(ls, disclosureLine{pack: []string{"a", "b"}[i%2], kind: k})
		}
		return sealedWithheldLine(ls, nil)
	}
	if got := line(); got != "" {
		t.Errorf("nothing withheld renders %q, want no line", got)
	}
	for _, c := range []struct {
		kinds []packdecl.Kind
		want  string
	}{
		{[]packdecl.Kind{packdecl.KindReadsHost}, "Sealed build: 1 host read declared by a is withheld"},
		{[]packdecl.Kind{packdecl.KindMount, packdecl.KindBriefing}, "Sealed build: 2 host reads declared by a, b are withheld"},
		{[]packdecl.Kind{packdecl.KindEnv, packdecl.KindLoophole, packdecl.KindLoophole},
			"Sealed build: 1 pack env var and 2 loophole crossings declared by a, b are withheld"},
		{[]packdecl.Kind{packdecl.KindEnv, packdecl.KindReadsHost, packdecl.KindLoophole},
			"Sealed build: 1 pack env var, 1 host read and 1 loophole crossing declared by a, b are withheld"},
	} {
		if got := line(c.kinds...); !strings.HasPrefix(got, c.want+" (FP-D9") {
			t.Errorf("withheld %v renders %q, want it to begin %q", c.kinds, got, c.want)
		}
	}
}

// THE USER'S mise_tools ARE COUNTED IN THE SAME LINE (FP-D19): alone, when no pack claim was
// withheld, and after the claims otherwise, each with its own reason. The end-to-end half, on a
// real sealed launch of a user config with mise_tools, is
// TestASealedBuildInstallsNoneOfTheUsersMiseTools.
func TestTheSealedWithheldLineCountsTheUsersMiseTools(t *testing.T) {
	env := []disclosureLine{{pack: "a", kind: packdecl.KindEnv}}
	for _, c := range []struct {
		claims []disclosureLine
		tools  []string
		want   string
	}{
		{nil, []string{"neovim"},
			"Sealed build: 1 of your mise_tools is withheld (FP-D19: a build line fetches any tool it needs)"},
		{nil, []string{"neovim", "jq"},
			"Sealed build: 2 of your mise_tools are withheld (FP-D19: a build line fetches any tool it needs)"},
		{env, []string{"neovim"},
			"Sealed build: 1 pack env var declared by a is withheld, as is 1 of your mise_tools " +
				"(FP-D9: a build jail gets no credential and no host file; FP-D19: a build line fetches any tool it needs)"},
		{append(env, disclosureLine{pack: "b", kind: packdecl.KindReadsHost}), []string{"neovim", "jq"},
			"Sealed build: 1 pack env var and 1 host read declared by a, b are withheld, as are 2 of your mise_tools " +
				"(FP-D9: a build jail gets no credential and no host file; FP-D19: a build line fetches any tool it needs)"},
		{env, nil, "Sealed build: 1 pack env var declared by a is withheld " +
			"(FP-D9: a build jail gets no credential and no host file)"},
	} {
		if got := sealedWithheldLine(c.claims, c.tools); got != c.want {
			t.Errorf("claims %v and tools %v render\n  %q\nwant\n  %q", c.claims, c.tools, got, c.want)
		}
	}
}
