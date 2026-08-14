package check

// audiopack_test.go is the R6 half of the `audio` pack's proof: selecting it leaves
// `yolo check` CLEAN (docs/design/loophole-packaging.md §7, OQ-LP11).
//
// It is a separate assertion from "the pack lints" and from "the pre-flight accepts it",
// because sectionPacks is the surface a user actually runs before launching, and it composes
// things the other two never touch: address parsing over an `embedded:` source, the selected
// set's collision pass, and the pack footprint. The first of those has already broken once
// for embedded packs (`pack address "embedded:claude" has no scheme` — a valid config
// reporting three failures), and this pack is the first embedded one whose contributions are
// neither an agent's config surface nor skills.

import (
	"bytes"
	"strings"
	"testing"
)

// Selecting the `audio` pack must produce zero check failures.
//
// The pack ships a `loophole` and an `env` contribution and NO config surface, no skills and
// no program — a shape no embedded pack had before it, and precisely the shape that a check
// written around agent packs could mishandle without anyone noticing.
func TestAudioPackSelectionPassesCheck(t *testing.T) {
	packsFixture(t, `{"packs": ["audio"]}`)

	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r)
	got := out.String()
	if r.failed != 0 {
		t.Errorf("selecting the audio pack must leave `yolo check` clean, got %d failure(s):\n%s",
			r.failed, got)
	}
	// Acknowledged BY NAME, for the reason TestEmbeddedPacksPassCheckWithoutAnAddress gives:
	// someone who wrote the key should see it took effect rather than wonder.
	if !strings.Contains(got, "audio") {
		t.Errorf("check should acknowledge the audio pack:\n%s", got)
	}
}

// The audio pack alongside an AGENT pack — the real-world selection, since a jail whose only
// pack is `audio` has no coding agent at all.
//
// This is the combination that would surface a collision between the pack's `env` keys (or
// its loophole name) and anything a shipped agent pack declares. There is none today, and
// that is worth pinning: the check's own collision pass runs over the SELECTED set, so a
// future clash would appear here first.
func TestAudioPackSelectionWithAnAgentPackPassesCheck(t *testing.T) {
	packsFixture(t, `{"packs": ["claude", "audio"]}`)

	var out bytes.Buffer
	r := newReporter(&out, false)
	(&Options{}).sectionPacks(r)
	if r.failed != 0 {
		t.Errorf("claude + audio must be a clean selection, got %d failure(s):\n%s",
			r.failed, out.String())
	}
}
