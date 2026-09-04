package cli

import (
	"bytes"
	"strings"
	"testing"
)

// packUsage is the destination the empty-packs notice sends a user to (see
// run.warnIfNoPacks and check.sectionPacks, which both print "run `yolo pack --help`").
// A user arriving from that notice has just been told their jail has no agent, so this
// text has to ANSWER "what do I put here" — not merely list authoring verbs. Without
// this test the notice can keep pointing at help that never mentions agents.
func TestPackUsageAnswersWhatPacksAreFor(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := packMain([]string{"--help"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack --help rc = %d: %s", rc, errw.String())
	}
	got := out.String()
	lower := strings.ToLower(got)

	// The subject the notice sends people here to read about.
	if !strings.Contains(lower, "agent") {
		t.Errorf("pack help never mentions agents — the notice points here for exactly "+
			"that:\n%s", got)
	}
	// The "NOT YET" honesty clause is GONE, deleted deliberately as its own comment
	// instructed: a pack can install an agent now, so the text that said it could not was
	// the thing that had become dishonest.
	//
	// What replaces it is the concrete answer a user arriving from the no-agent warning
	// needs. They have just been told their jail has no agent; help that discusses packs
	// abstractly leaves them exactly where they were. It must show the shortest working
	// entry, by name.
	if !strings.Contains(got, `"packs": ["claude"]`) {
		t.Errorf("pack help must show the shortest working entry — a user arriving from "+
			"the no-agent warning needs the literal thing to type:\n%s", got)
	}
	if strings.Contains(got, "NOT YET") {
		t.Errorf("pack help still claims a pack cannot install an agent; it can:\n%s", got)
	}
	// Where the list lives, and how to make an entry take effect. A user who reads
	// only this text must be able to get from zero packs to one.
	for _, want := range []string{
		"~/.config/yolo-jail/config.jsonc", // the file to edit
		`"packs"`,                          // the key
		"file://",                          // an address they can copy
		"yolo pack install",                // the step that fetches
	} {
		if !strings.Contains(got, want) {
			t.Errorf("pack help missing %q — a user cannot get from zero packs to one:\n%s",
				want, got)
		}
	}
	// config-ref stays the exhaustive schema; help must hand off rather than
	// duplicate it (and rot).
	if !strings.Contains(got, "yolo config-ref") {
		t.Errorf("pack help should hand off to `yolo config-ref` for the full schema:\n%s", got)
	}
}

// Every route to the help text shows the same thing: a bare `yolo pack`, an explicit
// --help/-h/help. A user told to run `yolo pack --help` must not get a different (or
// empty) answer than one who typed `yolo pack`.
func TestPackHelpRoutesAgree(t *testing.T) {
	render := func(t *testing.T, args ...string) string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := packMain(args, &out, &errw, false); rc != 0 {
			t.Fatalf("pack %v rc = %d: %s", args, rc, errw.String())
		}
		return out.String()
	}
	want := render(t, "--help")
	if want == "" {
		t.Fatal("pack --help printed nothing")
	}
	for _, args := range [][]string{{}, {"-h"}, {"help"}} {
		if got := render(t, args...); got != want {
			t.Errorf("pack %v help differs from `pack --help`:\n%s", args, got)
		}
	}
}

// The `pack` blurb in `yolo --help` must lead with adding an agent. With the `agents`
// config key gone, `pack` is the only route to a jail that has an agent in it, so a
// blurb about authoring alone leaves a new user with nowhere to go from the top level.
func TestUsageMentionsPacksAsTheAgentRoute(t *testing.T) {
	for _, c := range commandHelp {
		if c.name != "pack" {
			continue
		}
		if !strings.Contains(strings.ToLower(c.blurb), "agent") {
			t.Errorf("the `pack` blurb must name agents — it is the only way to get one: %q",
				c.blurb)
		}
		return
	}
	t.Error("no `pack` entry in commandHelp")
}
