package config

import (
	"strings"
	"testing"
)

// One clause undoes a selection for SEVERAL programs at once: the key respelled with a null for
// each, or the -p pairs for each, so a bare profile reaching nothing for nine programs gets one
// next step rather than nine.
func TestProfileDeselectionAllNamesEveryProgramInOneSpelling(t *testing.T) {
	h := newMountsHost(t)
	h.user(t, "{\n  \"profile\": \"bedrock\"\n}\n")
	key := ProfileSelection{Default: []string{"bedrock"}}

	got := ProfileDeselectionAll(key, ProfileSelection{}, []string{"agy", "copilot"})
	for _, want := range []string{
		"your config's `profile` key at ~/.config/yolo-jail/config.jsonc:2:14",
		"write `\"profile\": {\"*\": \"bedrock\", \"agy\": null, \"copilot\": null}` there",
		"to select none for agy, copilot on every launch",
		"or add `-p agy= -p copilot=` for one launch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ProfileDeselectionAll() = %q, want it to contain %q", got, want)
		}
	}
	flag := ProfileSelection{Default: []string{"bedrock"}}
	if got := ProfileDeselectionAll(key, flag, []string{"agy", "copilot"}); got != "the selection is this launch's `-p`, so add `-p agy= -p copilot=` to it" {
		t.Errorf("a -p selection was answered with %q", got)
	}
	if one, all := ProfileDeselection(key, ProfileSelection{}, "agy"), ProfileDeselectionAll(key, ProfileSelection{}, []string{"agy"}); one != all {
		t.Errorf("one program must read as before:\n%q\n%q", one, all)
	}
}
