package config

import (
	"strings"
	"testing"
)

// loopholenotinstalled_test.go pins the REMEDY carried by the two messages a
// `loopholes.<name>` entry earns when the name resolves to no loophole.
//
// # The situation the activation sprint created, and what it cost
//
// Before 2026-08-18 the four loopholes yolo ships were either BUNDLED (always
// present, so `loopholes.audio.enabled` always resolved) or not loopholes at all
// (`journal` and the cgroup delegate were builtin services behind their own
// top-level keys). Every one of them is a PACK-SHIPPED loophole now, which makes
// `installed` mean "the pack is selected" — and `packs` is user-scope only.
//
// So a WORKSPACE `yolo-jail.jsonc` that switches a pack loophole on before the user
// config selects the pack REFUSES THE LAUNCH (loadAndValidateConfig exits on any
// config error). That file is the one committed to a repo, so the population is a
// whole team on one shared config, and the remedy they were handed named neither
// half of the fix: it said to hand-write `command: ["<host daemon argv>"]` — an argv
// for a daemon yolo ships, in a file that may not install anything anyway.
//
// The USER-config half of the same situation was the mirror failure: a warning
// saying the entry "is a no-op", which is the silent-demotion outcome the retirement
// messages in validate.go exist to prevent one level up.
//
// These tests are about the REMEDY TEXT, which is unusual and deliberate. The
// refusal itself is correct; what made it a launch-stopper rather than a speed bump
// was that following its advice could not fix it.

// TestWorkspaceEnableOfUninstalledLoopholeNamesThePackRoute is the launch-refusing
// half. The error must send the reader to `packs` — the actual install for
// everything yolo ships — and must say that key is user-scope too, because the file
// being refused cannot write it either.
func TestWorkspaceEnableOfUninstalledLoopholeNamesThePackRoute(t *testing.T) {
	_, errs, _ := validateScoped(t, `{}`,
		`{"loopholes": {"journal": {"enabled": true}}}`, fakeResolver{})

	hits := containing(errs, "config.loopholes.journal", "not installed")
	if len(hits) != 1 {
		t.Fatalf("want exactly one enable-uninstalled error, got %v", errs)
	}
	msg := hits[0]
	for _, want := range []string{
		`"packs"`,          // the route that actually installs a pack loophole
		"user-scope",       // ...and the fact this file cannot take it
		"SHIPS IN A PACK",  // the condition under which that route is the one
		userConfigHintPath, // where both halves have to be written
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must mention %q, so a reader can act on it:\n%s", want, msg)
		}
	}
	// The old advice, in isolation, was the whole defect: a reader who followed it
	// installed a hand-written daemon under a name yolo answers to. It may still
	// appear as the SECOND branch ("if it ships in no pack"), so this asserts the
	// pack route is named FIRST rather than that the inline route is gone.
	if pack, inline := strings.Index(msg, `"packs"`), strings.Index(msg, "host daemon argv"); inline >= 0 && pack > inline {
		t.Errorf("the inline-command remedy precedes the pack remedy; a pack-shipped "+
			"loophole is the common case and must be answered first:\n%s", msg)
	}
}

// TestUserEnableOfUninstalledLoopholeCarriesTheSameRemedy is the other half: the
// user config CAN install, so this is a warning and the launch proceeds — with the
// loophole doing nothing. The line must therefore say what is missing, in the same
// words, or the two halves of one situation disagree about the fix.
func TestUserEnableOfUninstalledLoopholeCarriesTheSameRemedy(t *testing.T) {
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"journal": {"enabled": true}}}`, "", fakeResolver{})
	if len(errs) != 0 {
		t.Fatalf("a user config may install, so this must not refuse the launch: %v", errs)
	}
	hits := containing(warns, "config.loopholes.journal", "no loophole named")
	if len(hits) != 1 {
		t.Fatalf("want exactly one unknown-name warning, got %v", warns)
	}
	if !strings.Contains(hits[0], `"packs"`) {
		t.Errorf("the warning must name the pack route — otherwise a user who selected no "+
			"pack is told only that their entry does nothing:\n%s", hits[0])
	}
	// "this entry is a no-op" was the whole of the old diagnosis. It is true and
	// useless: it describes the absence instead of the fix, which is exactly the
	// silent-demotion shape validateJournalRetired refuses one level up.
	if strings.Contains(hits[0], "this entry is a no-op") {
		t.Errorf("the warning still stops at describing the absence:\n%s", hits[0])
	}
}

// TestRetiredLoopholeNameIsAnsweredWithItsReplacement is the sprint's standing rule
// applied to a loophole NAME rather than a config key: a name that was deleted must
// be answered by the name that replaced it.
//
// `audio-alsa` is the measured case. The audio pack shipped it only because the
// bundled `audio` loophole had reserved the plain name; when that loophole became
// the pack, the two merged under `audio` and `audio-alsa` stopped existing. Both
// scopes are asserted, because the failure was different in each: a workspace file
// got a launch refusal advising a hand-written daemon argv, and a user file got a
// warning that the entry was a no-op — neither ever saying the capability still
// exists under another name.
func TestRetiredLoopholeNameIsAnsweredWithItsReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, user, ws string
		pick           func(errs, warns []string) []string
	}{
		{
			name: "workspace scope refuses the launch",
			user: `{}`,
			ws:   `{"loopholes": {"audio-alsa": {"enabled": true}}}`,
			pick: func(errs, _ []string) []string { return errs },
		},
		{
			name: "user scope warns",
			user: `{"loopholes": {"audio-alsa": {"enabled": true}}}`,
			ws:   "",
			pick: func(_, warns []string) []string { return warns },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs, warns := validateScoped(t, tc.user, tc.ws, fakeResolver{})
			hits := containing(tc.pick(errs, warns), "config.loopholes.audio-alsa")
			if len(hits) != 1 {
				t.Fatalf("want exactly one message about the retired name, got errs=%v warns=%v", errs, warns)
			}
			msg := hits[0]
			for _, want := range []string{"RETIRED", "'audio'", `"packs": ["audio"]`} {
				if !strings.Contains(msg, want) {
					t.Errorf("a retired loophole name must be answered with its replacement "+
						"(missing %q):\n%s", want, msg)
				}
			}
			// The generic remedy would tell this reader to install `audio-alsa`
			// inline — resurrecting a name yolo deleted, under a config entry that
			// would shadow nothing and run whatever argv they invented.
			if strings.Contains(msg, `{"audio-alsa": {"command"`) {
				t.Errorf("the message offers to install the RETIRED name inline:\n%s", msg)
			}
		})
	}
}

// userConfigHintPath is the literal path both messages must name. Spelled out here
// rather than referencing loopholeUserConfigHint so a change to the constant has to
// be a deliberate change to this expectation too — the path is what a user types.
const userConfigHintPath = "~/.config/yolo-jail/config.jsonc"
