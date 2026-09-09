package run

import (
	"strings"
	"testing"
	"time"
)

// TestOfferIsNotConsumedByBeingGiven is OQ-BF1's ruling, and the case the first
// draft of this design got wrong: "offered once" would retire the prompt for
// classes that recur by NORMAL USE — a build cache grows by building — so a user
// who deferred once would never be asked again about a store that keeps growing.
func TestOfferIsNotConsumedByBeingGiven(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	big := int64(50) << 30

	deferred := offerRecord{Answer: OfferNotNow, When: now}
	if got := DecideOffer(deferred, big, now.Add(24*time.Hour)); got != OfferSkip {
		t.Errorf("a day after 'not now': %v, want skip — the deferral has a week on it", got)
	}
	if got := DecideOffer(deferred, big, now.Add(8*24*time.Hour)); got != OfferAsk {
		t.Fatalf("eight days after 'not now': %v, want ASK. An offer retired by being declined "+
			"once is the trigger defect one level up (OQ-BF1)", got)
	}
}

// TestOfferDecisionTable pins the rest of the rule, including the two answers
// that are easy to conflate: `never` and a live `not now` both skip, but only
// one of them ever comes back.
func TestOfferDecisionTable(t *testing.T) {
	now := time.Now()
	big, small := int64(50)<<30, int64(200)<<20
	cases := []struct {
		name string
		rec  offerRecord
		size int64
		want OfferDecision
		why  string
	}{
		{"never asked, over threshold", offerRecord{}, big, OfferAsk,
			"there is something to reclaim and nothing on record"},
		{"never asked, under threshold", offerRecord{}, small, OfferSkip,
			"200 MB is not worth a line in front of a jail start"},
		{"yes is permanent", offerRecord{Answer: OfferYes, When: now.Add(-time.Hour)}, big, OfferAutomatic,
			"consent given moves the class to the automatic tier, not to a fresh prompt"},
		{"yes applies even under threshold", offerRecord{Answer: OfferYes}, small, OfferAutomatic,
			"an automatic class does not re-enter the offered tier when it happens to be small"},
		{"never is permanent", offerRecord{Answer: OfferNever, When: now.Add(-9999 * time.Hour)}, big, OfferSkip,
			"the only answer that stops the asking without reclaiming"},
	}
	for _, c := range cases {
		if got := DecideOffer(c.rec, c.size, now); got != c.want {
			t.Errorf("%s: got %v, want %v — %s", c.name, got, c.want, c.why)
		}
	}
}

// TestUnderThresholdLeavesNoRecord: silence must not look like an answer. If a
// small class stamped anything, the next real offer would read as already
// handled.
func TestUnderThresholdLeavesNoRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := OfferAnswerFor("cache"); got.Answer != "" {
		t.Fatalf("a fresh machine already has an answer: %+v", got)
	}
	if got := DecideOffer(OfferAnswerFor("cache"), 10<<20, time.Now()); got != OfferSkip {
		t.Fatalf("got %v, want skip", got)
	}
	if got := OfferAnswerFor("cache"); got.Answer != "" {
		t.Fatalf("deciding wrote %+v — a skipped class must leave no record", got)
	}
}

// TestOfferAnswersRoundTripPerClass: the answers are per class and machine-wide.
// Per-workspace state would ask again in every workspace for a store that is
// shared by all of them.
func TestOfferAnswersRoundTripPerClass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now()
	RecordOfferAnswer("cache", OfferNever, now)
	RecordOfferAnswer("images", OfferYes, now)
	if got := OfferAnswerFor("cache").Answer; got != OfferNever {
		t.Errorf("cache = %q, want never", got)
	}
	if got := OfferAnswerFor("images").Answer; got != OfferYes {
		t.Errorf("images = %q, want yes", got)
	}
	if !strings.Contains(offerStatePath(), "yolo-jail") {
		t.Errorf("offer state at %q — it must live in the machine-wide state dir, because the "+
			"stores it answers for are machine-wide", offerStatePath())
	}
}

// TestUnrecognisedReplyIsNotConsent: the destructive branch must not be
// reachable by a stray keystroke, so anything unparsed reads as "not now".
func TestUnrecognisedReplyIsNotConsent(t *testing.T) {
	for _, in := range []string{"", "\n", "maybe", "Y E S", "yolo", "1"} {
		if got := parseOfferReply(in); got == OfferYes || got == OfferNever {
			t.Errorf("parseOfferReply(%q) = %q — an unrecognised reply must never reclaim, "+
				"and must never permanently opt out either", in, got)
		}
	}
	if parseOfferReply("y") != OfferYes || parseOfferReply("YES") != OfferYes {
		t.Error("an explicit yes must be honoured")
	}
	if parseOfferReply("v") != OfferNever || parseOfferReply("never") != OfferNever {
		t.Error("an explicit never must be honoured")
	}
}

// TestNonTTYNeverImpliesYes: a non-interactive launch prints and does nothing.
// The polarity config-safety.md OQ-D2 chose — never an implicit yes.
func TestNonTTYNeverImpliesYes(t *testing.T) {
	line := NonTTYOfferLine("cache files older than 30 d", "pants 39.4, uv 3.1", 49<<30)
	for _, want := range []string{"yolo prune --apply", "will not"} {
		if !strings.Contains(line, want) {
			t.Errorf("non-TTY line %q does not mention %q", line, want)
		}
	}
	if strings.Contains(strings.ToLower(line), "[y]es") {
		t.Error("the non-TTY line offers a prompt nobody can answer")
	}
}
