package run

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// offer.go is the OFFERED TIER (disk-levers-and-backfill.md OQ-BF1/OQ-BF2, §5.2
// and §5.3): the classes yolo will not reclaim without being told, because their
// regeneration is an unbounded re-fetch rather than a build or a load.
//
// THE OFFER IS NOT CONSUMED BY BEING GIVEN, which is OQ-BF1's ruling and the
// thing most likely to be "simplified" away. Three of the five backfill classes
// recur by normal use — a build cache grows by building — so an offer shown once
// and retired would be the same trigger defect one level up, and for the two
// one-shot classes the offer's RETURN is the bug report. Hence: offered whenever
// a class is over the threshold and no answer stands.

// OfferAnswer is what a user said about one class. The zero value is "never
// asked", which is deliberately distinct from every recorded answer.
type OfferAnswer string

const (
	// OfferYes moves the class to the automatic tier: reclaim now, and from now
	// on without asking.
	OfferYes OfferAnswer = "yes"
	// OfferNotNow defers. It carries a date, and expires — that is the whole
	// difference between it and OfferNever.
	OfferNotNow OfferAnswer = "not-now"
	// OfferNever opts the class out permanently. `yolo prune` stays available;
	// this is the only answer that stops the asking without reclaiming.
	OfferNever OfferAnswer = "never"
)

// offerReAskAfter is how long a "not now" holds. §5.3's number.
const offerReAskAfter = 7 * 24 * time.Hour

// OfferThreshold is the size below which a class is not worth interrupting a
// launch for. §5.3's number.
const OfferThreshold = 1 << 30 // 1 GiB

// offerRecord is one class's standing answer.
type offerRecord struct {
	Answer OfferAnswer `json:"answer"`
	When   time.Time   `json:"when"`
}

// offerStatePath is where the answers live: under the machine-wide state dir,
// because the classes are machine-wide. A per-workspace file would ask again
// per workspace, which is the same class of mistake as a per-workspace lock for
// a machine-wide store.
func offerStatePath() string {
	return filepath.Join(paths.BuildDir(), "offer-answers.json")
}

func readOfferState() map[string]offerRecord {
	out := map[string]offerRecord{}
	data, err := os.ReadFile(offerStatePath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

func writeOfferState(state map[string]offerRecord) {
	if err := os.MkdirAll(filepath.Dir(offerStatePath()), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	tmp := offerStatePath() + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, offerStatePath())
	}
}

// OfferDecision is what the launch should do about one class this time.
type OfferDecision int

const (
	// OfferSkip: nothing to say. Under threshold, opted out, or a live "not now".
	OfferSkip OfferDecision = iota
	// OfferAsk: prompt, because there is something to reclaim and no answer stands.
	OfferAsk
	// OfferAutomatic: the user already said yes; reclaim without asking.
	OfferAutomatic
)

// DecideOffer is the whole trigger rule, as a pure function so it can be tested
// without a terminal or a clock.
//
// The rule (§5.3, as amended by OQ-BF1): offer when the class is at or over the
// threshold AND no answer is on record, or the recorded answer is "not now" and
// at least offerReAskAfter old. `yes` makes it automatic forever; `never` stops
// the asking; under threshold says nothing at all, including no stamp.
func DecideOffer(rec offerRecord, reclaimable int64, now time.Time) OfferDecision {
	if rec.Answer == OfferYes {
		return OfferAutomatic
	}
	if reclaimable < OfferThreshold {
		// Below the bar is SILENT, not deferred: a class with 200 MB to reclaim
		// is not worth a line in front of a jail start, and stamping it would
		// make the next real offer look answered.
		return OfferSkip
	}
	switch rec.Answer {
	case OfferNever:
		return OfferSkip
	case OfferNotNow:
		if now.Sub(rec.When) < offerReAskAfter {
			return OfferSkip
		}
		return OfferAsk
	default: // never asked
		return OfferAsk
	}
}

// RecordOfferAnswer stores what the user said about a class.
func RecordOfferAnswer(class string, answer OfferAnswer, now time.Time) {
	state := readOfferState()
	state[class] = offerRecord{Answer: answer, When: now}
	writeOfferState(state)
}

// OfferAnswerFor reads a class's standing answer.
func OfferAnswerFor(class string) offerRecord { return readOfferState()[class] }

// parseOfferReply maps a typed reply to an answer. Anything unrecognised is
// "not now" — the conservative reading, because the destructive branch must
// never be reachable by a stray keystroke.
func parseOfferReply(s string) OfferAnswer {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return OfferYes
	case "v", "never":
		return OfferNever
	default:
		return OfferNotNow
	}
}

// offerPrompt is the one shape every offered class uses, so a user learns it
// once — and may meet it again, since consent is per class and is not consumed
// by being given (OQ-BF1).
func offerPrompt(class, detail string, bytes int64) string {
	return "Reclaimable on this machine (" + class + ", older than yolo's rules allow):\n" +
		"  " + prune.FmtBytes(bytes) + "   " + detail + "\n" +
		"Reclaim now? [y]es / [n]ot now (ask again in 7 days) / ne[v]er (yolo prune stays available) "
}

// NonTTYOfferLine is what a non-interactive launch prints instead of prompting:
// the size and the command, once, and no deletion. The polarity config-safety.md
// OQ-D2 chose for a non-interactive change — never an implicit yes.
func NonTTYOfferLine(class, detail string, bytes int64) string {
	return prune.FmtBytes(bytes) + " of " + class + " is reclaimable (" + detail + "). " +
		"Run `yolo prune --apply` to reclaim it; this launch will not."
}

// --- Measure late, offer early (§5.3) -------------------------------------
//
// A cache walk is NEVER run in front of a launch: §2.1 measured 369k files in
// the class this serves, and §5.3 gives the walk a 60 s budget for that reason.
// So the size a prompt shows comes from the PREVIOUS launch's housekeeping slot,
// stamped with its time — the slot measures, the next launch offers.

// offerMeasurement is one class's last measured reclaimable size.
type offerMeasurement struct {
	Bytes   int64     `json:"bytes"`
	Detail  string    `json:"detail"`
	When    time.Time `json:"when"`
	Partial bool      `json:"partial"`
}

func offerMeasurementPath() string {
	return filepath.Join(paths.BuildDir(), "offer-measurements.json")
}

// RecordOfferMeasurement is the SLOT's half: what this launch measured, for the
// next launch to offer on.
func RecordOfferMeasurement(class string, m offerMeasurement) {
	state := map[string]offerMeasurement{}
	if data, err := os.ReadFile(offerMeasurementPath()); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	state[class] = m
	if err := os.MkdirAll(filepath.Dir(offerMeasurementPath()), 0o755); err != nil {
		return
	}
	if data, err := json.MarshalIndent(state, "", "  "); err == nil {
		tmp := offerMeasurementPath() + ".tmp"
		if os.WriteFile(tmp, data, 0o644) == nil {
			_ = os.Rename(tmp, offerMeasurementPath())
		}
	}
}

// LastOfferMeasurement is the PRE-ATTACH half: what the last slot measured.
func LastOfferMeasurement(class string) offerMeasurement {
	state := map[string]offerMeasurement{}
	if data, err := os.ReadFile(offerMeasurementPath()); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	return state[class]
}

// cachePurgeClass is the one offered class today: the host cache age-purge, the
// largest measured backfill (49.34 GiB) and the one whose regeneration this doc
// cannot bound.
const cachePurgeClass = "cache files older than 30 d"

// maybeOfferReclaim runs BEFORE the container attaches (§5.3's trigger), on the
// size the last slot measured. It never walks anything itself.
//
// Returns true when the user consented, so the caller can reclaim in the slot.
func (o *Options) maybeOfferReclaim() bool {
	m := LastOfferMeasurement(cachePurgeClass)
	switch DecideOffer(OfferAnswerFor(cachePurgeClass), m.Bytes, o.Now()) {
	case OfferAutomatic:
		return true
	case OfferSkip:
		return false
	}
	detail := m.Detail
	if m.Partial {
		detail += " (partial — the walk hit its budget)"
	}
	if !o.IsTTYStdout() {
		// No prompt, no deletion, one line. Never an implicit yes.
		o.pr(o.Stderr).printf("[dim]%s[/dim]", NonTTYOfferLine(cachePurgeClass, detail, m.Bytes))
		return false
	}
	// The same shape preflight.go's config-change prompt uses: write, read one
	// line, and treat anything else as the non-destructive answer.
	if _, err := o.Stdout.Write([]byte(offerPrompt(cachePurgeClass, detail, m.Bytes))); err != nil {
		return false
	}
	reply := ""
	scanner := bufio.NewScanner(o.Stdin)
	if scanner.Scan() {
		reply = scanner.Text()
	}
	answer := parseOfferReply(reply)
	RecordOfferAnswer(cachePurgeClass, answer, o.Now())
	return answer == OfferYes
}
