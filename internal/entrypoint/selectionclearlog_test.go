package entrypoint

import (
	"bytes"
	"strings"
	"testing"
)

// The deselect clear's record (OQ-PSW4, docs/design/provider-switching.md, ruled "no print,
// except in verbose mode"): each key a deselect clears is noted in the boot log, and the
// terminal stays silent. These drive the real pi, opencode and zai packs across boots with
// the multi-boot harness, with a boot-log channel attached beside the terminal one.

func withBootLog(r *pioencodeRender) *bytes.Buffer {
	log := &bytes.Buffer{}
	r.e.LogOnly = log
	return log
}

// TestDeselectClearIsRecordedInTheBootLogOnly: a deselect that clears yolo's own writes
// notes one line per cleared key in the boot log, naming the agent, surface, key and value,
// and prints nothing about it on the terminal.
func TestDeselectClearIsRecordedInTheBootLogOnly(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zai","opencode":"zai"}`)

	log := withBootLog(r)
	r.errw.Reset()
	r.render(t, ``)

	for _, want := range []string{
		`selection: cleared pi/settings defaultProvider (was "zai")`,
		`selection: cleared pi/settings defaultModel (was "glm-5.3")`,
		`selection: cleared opencode/config model (was "zai/glm-5.3")`,
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("boot log lacks %q:\n%s", want, log.String())
		}
	}
	if strings.Contains(r.errw.String(), "selection: cleared") {
		t.Errorf("the clear reached the terminal, which the ruling keeps silent:\n%s", r.errw.String())
	}
}

// TestAKeptUserEditIsNotRecordedAsCleared: a value the user edited in the jail survives a
// deselect, so it is not a clear and gets no line; the key yolo wrote and nobody touched
// beside it still does.
func TestAKeptUserEditIsNotRecordedAsCleared(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zai"}`)
	r.edit(t, []string{".pi", "agent", "settings.json"}, "defaultModel", "glm-5.3-flash")

	log := withBootLog(r)
	r.render(t, ``)

	if strings.Contains(log.String(), "pi/settings defaultModel") {
		t.Errorf("a kept user edit was recorded as cleared:\n%s", log.String())
	}
	if !strings.Contains(log.String(), `selection: cleared pi/settings defaultProvider (was "zai")`) {
		t.Errorf("the untouched yolo-written key beside the edit was not recorded:\n%s", log.String())
	}
}

// TestNoDeselectRecordsNothing: a boot that keeps its selection, and a fresh boot that never
// had one, clear nothing and record nothing.
func TestNoDeselectRecordsNothing(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	log := withBootLog(r)
	r.render(t, ``)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zai","opencode":"zai"}`)
	r.render(t, `{"pi":"zai","opencode":"zai"}`)
	if strings.Contains(log.String(), "selection: cleared") {
		t.Errorf("a boot with no deselect recorded a clear:\n%s", log.String())
	}
}
