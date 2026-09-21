package packdecl

import (
	"strings"
	"testing"
)

// retiredHookManifest is the manifest a pack in the wild still ships: the `claude_plugins`
// hook yolo's own claude pack declared until 2026-09-20, beside a live hook so every test
// here can check that retiring one name did not retire the kind.
const retiredHookManifest = `{"name":"x","contributes":[
	{"kind":"hook","hook":"claude_plugins"},
	{"kind":"hook","hook":"per_jail_history","from":".x/history.jsonl"}]}`

// A RETIRED HOOK NAMES ITS REPLACEMENT INSTEAD OF FALLING TO "unknown hook".
//
// `claude_plugins` was in a manifest yolo SHIPPED, so the generic diagnostic is the wrong
// answer twice over: it reads identically whether the name never existed or was
// deliberately taken away, and it leaves an author who copied a working pack with nothing
// to write instead. The ruling (docs/design/pi-pack-extensions.md OQ-2, 2026-09-19) is
// that nothing like it replaces it, which makes the migration text the ONLY route from
// the old declaration to a working one — so it has to be in the refusal.
//
// The fourth member of the retired-declaration set, after validate.go's
// `journal`/`host_processes`, retiredFieldProblems and retiredKinds.
func TestTheRetiredClaudePluginsHookRefusesWithItsMigration(t *testing.T) {
	_, problems := Decode([]byte(retiredHookManifest))
	if len(problems) == 0 {
		t.Fatal(`hook "claude_plugins" was accepted — it is retired, and a manifest still ` +
			"declaring it must hear so rather than have the hook silently not run")
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		"claude_plugins",      // the name the author wrote
		"REMOVED",             // that it was taken away, not mistyped
		"pi-pack-extensions",  // where the ruling is
		"Agent Plugins 1.0",   // the replacement the ruling names
		"no agent-named hook", // and the part of the ruling that forbids a look-alike
		"contributes[0]",      // located at the entry the author has to edit
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not contain %q — it must name the retirement and "+
				"what replaces it:\n%s", want, joined)
		}
	}
	// ONE MESSAGE, NOT TWO. retiredKinds settled this trade for kinds and knownHook keeps
	// it here: being told a declaration is both "retired (write this instead)" and
	// "unknown (here is the whole vocabulary)" is two contradictory instructions about one
	// line. This is also the assertion that fails if knownHook stops admitting the name.
	if strings.Contains(joined, "unknown hook") {
		t.Errorf("a retired hook ALSO got the unknown-hook message, so the author is told "+
			"two different things about one line:\n%s", joined)
	}
	if len(problems) != 1 {
		t.Errorf("want exactly one problem for one retired declaration, got %d:\n%s",
			len(problems), joined)
	}
	// And it is genuinely out of the closed set, so nothing on the host validates a NEW
	// manifest into declaring it.
	for _, k := range KnownHooks {
		if k == "claude_plugins" {
			t.Error("claude_plugins is still in KnownHooks, so the refusal above is the " +
				"only thing that changed")
		}
	}
}

// THE JAIL STILL BOOTS. Same asymmetry the retired KIND keeps, and it bites harder here:
// the manifest carrying this hook is one yolo itself staged last week, so an older staged
// tree read by a newer entrypoint is the NORMAL skew, not a corruption. The boot path
// treats every problem as fatal (A12), so a refusal on this path is a jail that will not
// start over a declaration the in-jail user cannot edit.
func TestARetiredHookIsSkippedNotRefusedAcrossTheVersionBoundary(t *testing.T) {
	m, problems, skipped := DecodeTolerant([]byte(retiredHookManifest))
	if len(problems) != 0 {
		t.Fatalf("a retired hook became a PROBLEM on the tolerant path, and the boot "+
			"treats every problem as fatal: %v", problems)
	}
	if len(skipped) != 1 {
		t.Fatalf("want one skip note for the one retired hook, got %v", skipped)
	}
	for _, want := range []string{"retired", "claude_plugins", "Agent Plugins 1.0"} {
		if !strings.Contains(skipped[0], want) {
			t.Errorf("the skip note must contain %q: %s", want, skipped[0])
		}
	}
	// It must not repeat the unknown-KIND promise that a newer build will render it. No
	// build will, and a reader sent looking for a newer yolo is worse off than one told so.
	if strings.Contains(skipped[0], "version skew") {
		t.Errorf("the skip note promises a newer build will honor a hook no build will "+
			"honor again: %s", skipped[0])
	}
	// THE CONTRIBUTION IS DROPPED, not merely reported. A kept one reaches
	// entrypoint.runPackHook, whose default arm fails the boot — which is the fatal this
	// skip exists to avoid, arriving one step later.
	for _, c := range m.Contributes {
		if c.Hook == "claude_plugins" {
			t.Error("the retired hook survived into the decoded manifest, so the boot " +
				"still dispatches it and still fails")
		}
	}
	// The LIVE hook beside it is untouched: retiring one name must not retire the kind.
	var live int
	for _, c := range m.Contributes {
		if c.Kind == KindHook && c.Hook == "per_jail_history" {
			live++
		}
	}
	if live != 1 {
		t.Errorf("the live per_jail_history hook was dropped with the retired one: %+v",
			m.Contributes)
	}
}

// The two surviving hooks still validate, on BOTH paths. Without this, deleting the whole
// `hook` case — or emptying KnownHooks — passes every test above.
func TestTheSurvivingHooksStillValidate(t *testing.T) {
	const live = `{"name":"x","contributes":[
		{"kind":"state","at":".x-shared","scope":"machine","because":"the shared tier"},
		{"kind":"hook","hook":"shared_credentials","from":".x/creds.json","at":".x-shared"},
		{"kind":"hook","hook":"per_jail_history","from":".x/history.jsonl"}]}`
	m, problems := Decode([]byte(live))
	if len(problems) != 0 {
		t.Fatalf("a manifest declaring only live hooks was refused: %v", problems)
	}
	if got := len(m.HookContributions()); got != 2 {
		t.Errorf("HookContributions() = %d, want the 2 live hooks", got)
	}
	if _, problems, skipped := DecodeTolerant([]byte(live)); len(problems) != 0 ||
		len(skipped) != 0 {
		t.Errorf("the tolerant path refused or skipped a live hook: %v / %v",
			problems, skipped)
	}
}

// RetiredHook answers only for a name this build removed. A map lookup that also answered
// for a LIVE name would turn every shipped hook into a refusal.
func TestRetiredHookAnswersOnlyForARetiredName(t *testing.T) {
	if RetiredHook("claude_plugins") == "" {
		t.Error("RetiredHook has no message for the one retired hook")
	}
	for _, k := range append([]string{"", "not_a_hook_anyone_wrote"}, KnownHooks...) {
		if msg := RetiredHook(k); msg != "" {
			t.Errorf("RetiredHook(%q) returned a retirement message: %s", k, msg)
		}
	}
}
