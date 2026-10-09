package macosuser

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE THREE DENIES TAKEN FROM AGENT SAFEHOUSE (docs/research/agent-safehouse.md §8.2),
// and the convention that makes each one greppable from its proof.
//
// WHAT THESE TESTS CAN AND CANNOT SETTLE. They are string tests, and a string test is
// exactly what §8.1 says this backend had too much of: the 16 Seatbelt tests here pin
// what the generator emits, and no kernel is asked whether the emitted text denies
// anything. What they DO settle is the half that a runtime test cannot see from inside
// a refusal — ORDERING. SBPL is last-match-wins, so a `(deny process-info-pidinfo)`
// placed above the `(allow process-info*)` this profile has always carried would be
// silently overridden: the directive would be in the file, the kernel would ignore it,
// and a runtime case asserting a refusal would fail with no indication why. These pin
// the order; integration/macosuserseatbelt_test.go pins the effect.

// seatbeltTestID matches the marker each deny carries. One capture group: the id.
var seatbeltTestID = regexp.MustCompile(`#seatbelt-test-id:([a-z0-9-]+)#`)

// TestEverySeatbeltDenyCarriesATestID is the convention, made mechanical.
//
// The rule is narrow on purpose: EVERY `(deny …)` line in the generated profile must be
// immediately preceded by a comment carrying an id. A deny with no id is a rule whose
// proof nobody can find, and — because the registry in
// integration/macosuserseatbelt_test.go is keyed on these ids — it is also a rule that
// registry will never notice is unproven. Re-allows carry ids too where a deny depends
// on one, but they are not required to: an allow that goes missing shows up as a broken
// agent, while a deny that goes missing shows up as nothing at all.
func TestEverySeatbeltDenyCarriesATestID(t *testing.T) {
	// The readonly entry and delivered content are passed so the config- and pack-driven
	// denies are in the text too — otherwise the denies a launch generates are the ones this
	// never checks.
	lines := strings.Split(SeatbeltProfile("/Users/Shared/proj", "", []string{"vendored"},
		sampleHomeReadonly()), "\n")
	seen := map[string]int{}
	denies := 0
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), "(deny ") {
			continue
		}
		denies++
		if i == 0 {
			t.Errorf("line %d is a deny with nothing above it: %s", i+1, ln)
			continue
		}
		m := seatbeltTestID.FindStringSubmatch(lines[i-1])
		if m == nil {
			t.Errorf("the deny on line %d carries no #seatbelt-test-id:…# on the line "+
				"above it, so nothing can name what proves it:\n  %s\n  %s",
				i+1, lines[i-1], ln)
			continue
		}
		seen[m[1]]++
	}
	if denies == 0 {
		t.Fatal("no deny directives found at all — the profile is not what this test thinks it is")
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("id %q labels %d denies; an id names one rule, or the registry "+
				"cannot say which one a proof covers", id, n)
		}
	}
}

// TestSeatbeltDeniesCrossProcessArgv pins the pair AND their position.
//
// The position is the whole risk. `(allow process-info*)` and `(allow sysctl-read)`
// have been at the end of this profile since it was written, `process-info*` INCLUDES
// `process-info-pidinfo`, and last match wins — so these denies are inert anywhere
// above them. That failure is invisible in the artifact: the directives are right
// there in the file a human reads.
func TestSeatbeltDeniesCrossProcessArgv(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", nil, HomeReadonly{})
	for _, want := range []string{
		`(deny sysctl-read (sysctl-name-regex #"procargs"))`,
		"(deny process-info-pidinfo)",
		"(allow process-info-pidinfo (target same-sandbox))",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n%s", want, p)
		}
	}
	mustPrecede(t, p, "(allow process-info*)", "(deny process-info-pidinfo)",
		"process-info* includes pidinfo, so the deny is inert above it")
	mustPrecede(t, p, "(allow sysctl-read)", `(deny sysctl-read (sysctl-name-regex #"procargs"))`,
		"the blanket sysctl-read allow overrides the procargs deny above it")
	mustPrecede(t, p, "(deny process-info-pidinfo)", "(allow process-info-pidinfo (target same-sandbox))",
		"the same-sandbox re-allow is inert above the deny it re-allows")
}

// TestSeatbeltDeniesSystemKeychains: /Library/Keychains was denied and
// /System/Library/Keychains was not, which agent-safehouse.md §3.2 found while
// comparing the two profiles. Both now are, and neither is re-allowed later.
func TestSeatbeltDeniesSystemKeychains(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", nil, HomeReadonly{})
	for _, want := range []string{
		`(deny file-read* (subpath "/Library/Keychains"))`,
		`(deny file-read* (subpath "/System/Library/Keychains"))`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("profile missing %q\n%s", want, p)
		}
	}
	// Nothing after them re-opens file reads wholesale. The /Users deny has a
	// re-allow of its own and it names literals and subpaths under /Users only, so a
	// blanket `(allow file-read*` appearing after the keychain denies would be a new
	// and silent widening.
	tail := p[strings.Index(p, `(deny file-read* (subpath "/System/Library/Keychains"))`):]
	if strings.Contains(tail, "(allow file-read* (subpath \"/\"))") ||
		strings.Contains(tail, "(allow file-read*)") {
		t.Errorf("a blanket file-read allow follows the keychain denies — last match "+
			"wins, so it reopens them\n%s", tail)
	}
}

// TestSeatbeltRestrictsIoctlToTerminals. The re-allow set is the part that decides
// whether an agent's TUI still works, so it is pinned by name rather than by "some
// allow follows the deny": dropping /dev/ptmx alone would leave every pty an agent's
// tooling allocates without its ioctls, which is a broken terminal rather than a
// refused one.
func TestSeatbeltRestrictsIoctlToTerminals(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", nil, HomeReadonly{})
	if !strings.Contains(p, "(deny file-ioctl)") {
		t.Errorf("profile does not deny file-ioctl\n%s", p)
	}
	for _, want := range []string{
		`(literal "/dev/tty")`,
		`(literal "/dev/ptmx")`,
		`(regex #"^/dev/ttys[0-9]")`,
		`(regex #"^/dev/pty[a-z0-9]")`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the file-ioctl re-allow is missing %q — a terminal the agent "+
				"opens under that name would lose its ioctls\n%s", want, p)
		}
	}
	mustPrecede(t, p, "(deny file-ioctl)", "(allow file-ioctl",
		"the tty re-allow is inert above the deny, and the agent would have no terminal at all")
}

// TestSeatbeltNewDeniesFollowTheWritableSet keeps the readonly-ordering invariant true
// for the block appended after it: the three denies added in 2026-09 sit at the END of
// the profile, past every file-write directive, so
// TestSeatbeltProfileHasNoWriteAllowAfterReadonlyDenies stays a statement about the
// whole profile rather than about the part of it that existed when it was written.
func TestSeatbeltNewDeniesFollowTheWritableSet(t *testing.T) {
	p := SeatbeltProfile("/Users/Shared/proj", "", []string{"vendored"}, HomeReadonly{})
	last := strings.LastIndex(p, "(allow file-write*")
	if last < 0 {
		t.Fatalf("no file-write allow in the profile\n%s", p)
	}
	for _, later := range []string{
		"(deny process-info-pidinfo)",
		"(deny file-ioctl)",
		`(deny file-read* (subpath "/System/Library/Keychains"))`,
	} {
		if strings.Index(p, later) < last {
			t.Errorf("%q precedes the last file-write allow; the profile's write policy "+
				"is meant to read as one unit ending at the readonly denies", later)
		}
	}
}

// macosLogDenyRules are the two unified-log rules every profile carries, by their text.
var macosLogDenyRules = []string{
	"#seatbelt-test-id:macos-log-deny#",
	`(subpath "/private/var/db/diagnostics")`,
	`(subpath "/private/var/db/uuidtext")`,
	"#seatbelt-test-id:macos-log-stream-deny#",
	`(deny mach-lookup (global-name "com.apple.diagnosticd"))`,
}

// TestSeatbeltAlwaysDeniesTheLog: the log is the macos-log bridge's (packs/macos-log), never the
// sandbox's, so every profile denies its stores and its stream service — the bare default and one
// with context mounts and devices alike. There is no setting left that removes the rules.
func TestSeatbeltAlwaysDeniesTheLog(t *testing.T) {
	for name, p := range map[string]string{
		"default": SeatbeltProfile("/Users/Shared/proj", "", []string{"vendored"}, HomeReadonly{}),
		"with context and devices": SeatbeltProfileWithContext("/Users/Shared/proj", "", nil, sampleHomeReadonly(),
			[]ContextLink{{Dest: "/ctx/lib", Source: "/Users/Shared/ci/lib", Dir: true}}, []string{"/dev/cu.usbserial-1"}),
	} {
		for _, want := range macosLogDenyRules {
			if !strings.Contains(p, want) {
				t.Errorf("%s: the profile lacks %q\n%s", name, want, p)
			}
		}
	}
}

// TestSeatbeltMacosLogDenyIsNotReopened: SBPL is last-match-wins, so a file-read allow AFTER
// the store deny whose filter covers /private/var/db would make it inert, with the directive
// still in the file. Nothing after it may allow a file read or a mach lookup at all.
func TestSeatbeltMacosLogDenyIsNotReopened(t *testing.T) {
	p := SeatbeltProfileWithContext("/Users/Shared/proj", "", []string{"vendored"}, sampleHomeReadonly(),
		[]ContextLink{{Dest: "/ctx/lib", Source: "/Users/Shared/ci/lib", Dir: true}},
		[]string{"/dev/cu.usbserial-1"})
	at := strings.Index(p, "#seatbelt-test-id:macos-log-deny#")
	if at < 0 {
		t.Fatalf("no unified-log deny in the profile\n%s", p)
	}
	tail := p[at:]
	for _, reopen := range []string{"(allow file-read", "(allow mach-lookup", "(allow default"} {
		if strings.Contains(tail, reopen) {
			t.Errorf("%q follows the unified-log deny and can re-open it\n%s", reopen, tail)
		}
	}
	mustPrecede(t, p, "#seatbelt-test-id:context-read-allow#", "#seatbelt-test-id:macos-log-deny#",
		"a context source's read allow below the deny would re-open the store for a source naming it")
	mustPrecede(t, p, "#seatbelt-test-id:workspace-read-allow#", "#seatbelt-test-id:macos-log-deny#",
		"the workspace read allow below the deny would be a later match")
}

// THE CALL SITE: the profile a launch installs carries the deny, whatever the config says —
// including a config still naming the retired `macos_log` key, which the pre-flight refuses
// on the host and only warns about in a jail's snapshot.
func TestBuildRunPlanAlwaysDeniesTheLog(t *testing.T) {
	for _, mode := range []any{nil, "off", "user", "full"} {
		cfg := jsonx.NewOrderedMap()
		if mode != nil {
			cfg.Set("macos_log", mode)
		}
		p := BuildRunPlan("/Users/Shared/proj", cfg, nil, []string{"bash"}, "/usr/local/bin/yolo", "",
			HomeOverlay{}, HostContext{}, jsonx.NewOrderedMap(), nil, nil)
		if !strings.Contains(p.Seatbelt, "#seatbelt-test-id:macos-log-deny#") {
			t.Errorf("macos_log %#v: the launch profile does not deny the log", mode)
		}
	}
}

// mustPrecede asserts that first appears before second, reporting why it matters.
func mustPrecede(t *testing.T, profile, first, second, why string) {
	t.Helper()
	a, b := strings.Index(profile, first), strings.Index(profile, second)
	if a < 0 || b < 0 {
		t.Fatalf("profile is missing %q (%d) or %q (%d)\n%s", first, a, second, b, profile)
	}
	if a >= b {
		t.Errorf("%q (at %d) must precede %q (at %d): %s", first, a, second, b, why)
	}
}
