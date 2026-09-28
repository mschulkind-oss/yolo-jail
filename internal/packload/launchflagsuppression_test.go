package packload_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE FLAG-ALIAS MAP IS DELETED, AND THIS IS THE BEHAVIOUR THAT CHANGED.
//
// A `launch` contribution could declare `{"--yolo": ["-y"]}`, and InjectLaunchFlags then
// skipped `--yolo` for a user who had typed `-y`. The map was a pack restating a fact about
// copilot's OWN flag parser — that the two spellings are one switch — in a second place, read
// only in order NOT to act, so the day copilot renamed the short option the stale entry would
// have produced silence rather than an error.
//
// What replaces it is nothing: yolo injects the declared flag and copilot's parser, the only
// parser that ever actually knew the two were one switch, resolves the pair.
//
// PINNED AS AN ASSERTION ABOUT THE SHIPPED PACK, not about a fixture, because the failure this
// guards against is someone re-adding the map to make this line "tidy" — which would restore a
// second, drifting copy of another tool's CLI grammar. If copilot ever rejects the duplicate
// pair outright, the answer is still not an alias table: it is that the flag stops being
// injected when the user has expressed the same intent, which needs copilot's parser and not
// yolo's guess.
func TestTypingTheShortSpellingNoLongerSuppressesTheInjectedFlag(t *testing.T) {
	packs := loadAll(t)

	rewritten, _ := packload.InjectLaunchFlags(packs, true, []string{"copilot", "-y", "chat"})
	got := strings.Join(rewritten, " ")
	if want := "copilot --yolo -y chat"; got != want {
		t.Errorf("got %q, want %q — `-y` is no longer known to yolo as a spelling of `--yolo`, "+
			"so the declared flag is injected and both reach copilot", got, want)
	}

	// The suppression that SURVIVES, against the same shipped declaration: the identical
	// flag. That one needs no knowledge of copilot's grammar — it compares the flag yolo is
	// about to add with the ones already there — which is the whole reason it stays.
	rewritten, _ = packload.InjectLaunchFlags(packs, true, []string{"copilot", "--yolo", "chat"})
	got = strings.Join(rewritten, " ")
	if want := "copilot --yolo chat"; got != want {
		t.Errorf("got %q, want %q — a flag already in the argv must not be injected twice", got, want)
	}
}

// AND THE MANIFEST MAY NOT CARRY ONE AGAIN. Deleting the field without pinning its absence
// leaves `"aliases"` as prose a pack author can still write: the strict decoder refuses it
// today, and this is what says that refusal is intended rather than incidental. Declared on
// `autonomy`, the kind that now owns every launch flag — the kind `aliases` used to sit on is
// itself retired, and its own refusal would otherwise be what this test measured.
func TestAliasesIsNotAManifestFieldAnyMore(t *testing.T) {
	_, problems := packdecl.Decode([]byte(
		`{"name":"x","contributes":[{"kind":"autonomy","autonomous":{"launch":[` +
			`{"bin":"x","flags":["--f"]}]},"aliases":{"--f":["-f"]}}]}`,
	))
	if len(problems) == 0 {
		t.Fatal("a manifest declaring `aliases` was accepted — the flag-alias map is deleted, " +
			"so a pack that still declares one must hear about it rather than have it ignored")
	}
	if !strings.Contains(strings.Join(problems, "\n"), "aliases") {
		t.Errorf("the refusal does not name the offending field: %v", problems)
	}
}
