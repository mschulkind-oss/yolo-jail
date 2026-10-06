package setupcensus

// census_test.go is the census's drift gate, in render/configkeys_test.go's three directions:
// every live key and kind is classified on every setup, nothing classified is a key or kind
// the schema lacks, and every cell says why. The enumerations are the code's own authorities,
// config.TopLevelConfigKeys() and packdecl.KnownKinds(), never a list kept here, so the gate
// fails the moment the schema grows: that is the call site this test pins.

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// gapMessage is what a missing cell tells the next author to do.
const gapMessage = "Every live top-level config key and every pack contribution kind needs a " +
	"census entry in internal/setupcensus (configkeys.go or kinds.go) with a cell for each of " +
	"podman/Linux, podman/macOS, container/macOS and macos-user/macOS: one of Honored, " +
	"HonoredBy (name the mechanism), Warned (name the launch line), Dropped, Refused or " +
	"NotApplicable, and a reason naming the code path. Then give it a row in " +
	"userguide/reference/settings-per-setup.md and name that row in the entry's Guide list."

// missingCells lists "<subject> on <setup>" for every setup the entry leaves unclassified, or
// every setup when there is no entry at all.
func missingCells(subject string, e Entry, ok bool) []string {
	var out []string
	for _, s := range Setups() {
		if !ok || e.Cell(s).Disposition == Unclassified {
			out = append(out, subject+" on "+s.String())
		}
	}
	return out
}

// TestEveryConfigKeyHasACellOnEverySetup is the forcing function: a key added to the schema
// fails the build until the census decides what each setup does with it.
func TestEveryConfigKeyHasACellOnEverySetup(t *testing.T) {
	var gaps []string
	for _, key := range config.TopLevelConfigKeys() {
		e, ok := ConfigKey(key)
		gaps = append(gaps, missingCells("config key `"+key+"`", e, ok)...)
	}
	if len(gaps) > 0 {
		t.Fatalf("the setup census has no answer for:\n  %s\n\n%s", strings.Join(gaps, "\n  "), gapMessage)
	}
}

// TestEveryPackKindHasACellOnEverySetup is the same gate over the manifest decoder's kinds.
func TestEveryPackKindHasACellOnEverySetup(t *testing.T) {
	var gaps []string
	for _, k := range packdecl.KnownKinds() {
		e, ok := Kind(k)
		gaps = append(gaps, missingCells("pack kind `"+string(k)+"`", e, ok)...)
	}
	if len(gaps) > 0 {
		t.Fatalf("the setup census has no answer for:\n  %s\n\n%s", strings.Join(gaps, "\n  "), gapMessage)
	}
}

// TestTheCensusHasNoPhantomEntries is the other direction: a key retired from the schema (or a
// kind retired from the decoder) must not leave an entry nothing reads.
func TestTheCensusHasNoPhantomEntries(t *testing.T) {
	live := map[string]bool{}
	for _, k := range config.TopLevelConfigKeys() {
		live[k] = true
	}
	var phantom []string
	for _, k := range ConfigKeys() {
		if !live[k] {
			phantom = append(phantom, "config key `"+k+"`")
		}
	}
	liveKinds := map[packdecl.Kind]bool{}
	for _, k := range packdecl.KnownKinds() {
		liveKinds[k] = true
	}
	for _, k := range Kinds() {
		if !liveKinds[k] {
			phantom = append(phantom, "pack kind `"+string(k)+"`")
		}
	}
	sort.Strings(phantom)
	if len(phantom) > 0 {
		t.Errorf("the census classifies what the schema does not have: %v — drop the entry, or "+
			"add the key or kind to its authority", phantom)
	}
}

// eachCell visits every cell of every entry and aspect, naming each by its path.
func eachCell(visit func(path string, s Setup, c Cell)) {
	var walk func(path string, e Entry)
	walk = func(path string, e Entry) {
		for _, s := range Setups() {
			visit(path, s, e.Cell(s))
		}
		for name, a := range e.Aspects {
			walk(path+"."+name, a)
		}
	}
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		walk("config key "+k, e)
	}
	for _, k := range Kinds() {
		e, _ := Kind(k)
		walk("pack kind "+string(k), e)
	}
}

// TestEveryCellHasADispositionAndAReason: a disposition with no reason cannot be re-decided,
// which is inherit.go's argument and holds for Honored cells too. Aspects are held to it as
// well, since a reader reaches them by the same lookups.
func TestEveryCellHasADispositionAndAReason(t *testing.T) {
	eachCell(func(path string, s Setup, c Cell) {
		if c.Disposition == Unclassified {
			t.Errorf("%s on %s has no disposition", path, s)
		}
		if strings.TrimSpace(c.Reason) == "" {
			t.Errorf("%s on %s has no reason", path, s)
		}
	})
}

// TestAnAspectDiffersFromItsParent: an aspect exists to say where a sub-mechanism parts from
// its key, so one that agrees with its parent on every setup is a second copy of the parent's
// answer, free to drift from it.
func TestAnAspectDiffersFromItsParent(t *testing.T) {
	check := func(path string, parent Entry) {
		for name, a := range parent.Aspects {
			differs := false
			for _, s := range Setups() {
				if a.Cell(s).Disposition != parent.Cell(s).Disposition {
					differs = true
				}
			}
			if !differs {
				t.Errorf("%s.%s has its parent's disposition on every setup: drop the aspect "+
					"and let the parent's Guide list claim its rows", path, name)
			}
			if len(a.Aspects) > 0 {
				t.Errorf("%s.%s has aspects of its own; the census is two levels deep", path, name)
			}
		}
	}
	for _, k := range ConfigKeys() {
		e, _ := ConfigKey(k)
		check("config key "+k, e)
	}
	for _, k := range Kinds() {
		e, _ := Kind(k)
		check("pack kind "+string(k), e)
	}
}

// A HonoredBy cell must name its mechanism — §3's warning is that the un-named version of the
// sentence is what hid #39 — and a Warned cell must say where the launch says so. The cheapest
// check that catches an empty gesture at either is that the reason names some code: a function,
// a file, or a measurement on record.
func TestHonoredByAndWarnedCellsNameWhere(t *testing.T) {
	eachCell(func(path string, s Setup, c Cell) {
		if c.Disposition != HonoredBy && c.Disposition != Warned {
			return
		}
		if !namesCode(c.Reason) {
			t.Errorf("%s on %s is %s and its reason names no code path, file or measurement: %q",
				path, s, c.Disposition, c.Reason)
		}
	})
}

// namesCode reports whether a reason cites something a reader can go and look at: a Go
// identifier spelled with a call or a dot, a file name, or the word "measured".
func namesCode(reason string) bool {
	for _, tell := range []string{".go", "()", "measured", "Measured"} {
		if strings.Contains(reason, tell) {
			return true
		}
	}
	for _, w := range strings.Fields(reason) {
		w = strings.Trim(w, "(),;:`'\"")
		if i := strings.Index(w, "."); i > 0 && i < len(w)-1 && isIdentStart(w[0]) && isIdentStart(w[i+1]) {
			return true
		}
		if hasInnerUpper(w) {
			return true
		}
	}
	return false
}

func isIdentStart(b byte) bool { return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// hasInnerUpper spots a camelCase Go identifier such as noteMacosUserPortKeys.
func hasInnerUpper(w string) bool {
	if len(w) < 4 {
		return false
	}
	for i := 1; i < len(w); i++ {
		if w[i] >= 'A' && w[i] <= 'Z' && w[i-1] >= 'a' && w[i-1] <= 'z' {
			return true
		}
	}
	return false
}

// The vocabulary is §3's six, in §3's order, and each spells the word a `// parity:` marker
// carries — internal/cli/run's annotation census reads this list.
func TestTheDispositionsAreSection3s(t *testing.T) {
	var got []string
	for _, d := range Dispositions() {
		got = append(got, d.String())
	}
	want := "Honored HonoredBy Warned Dropped Refused NotApplicable"
	if strings.Join(got, " ") != want {
		t.Errorf("Dispositions() = %v, want %s", got, want)
	}
	if Unclassified.Works() || Warned.Works() || !Honored.Works() || !HonoredBy.Works() {
		t.Error("Works() must be true for Honored and HonoredBy only")
	}
}

// Find and Paths are the census's addresses for readers outside the package; every path Paths
// lists must resolve, and a kind path must not resolve to the config key of the same name.
func TestEveryPathResolves(t *testing.T) {
	for _, p := range Paths() {
		if _, ok := Find(p); !ok {
			t.Errorf("Paths() lists %q and Find cannot resolve it", p)
		}
	}
	key, _ := Find("profile")
	kind, _ := Find(KindPathPrefix + "profile")
	if key.PodmanLinux.Reason == kind.PodmanLinux.Reason {
		t.Error("Find(\"profile\") and Find(\"kind:profile\") gave the same entry")
	}
	if _, ok := Find("resources.no_such_aspect"); ok {
		t.Error("Find resolved an aspect the census does not have")
	}
}
