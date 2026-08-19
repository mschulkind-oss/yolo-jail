package config

import (
	"strings"
	"testing"
)

// cgroupdelegate_test.go pins a REFUSAL THAT WAS DELETED, which needs a test more than
// most deletions do: nothing else in the suite covered the rule, so removing it left no
// failure and no evidence either way.
//
// `config.loopholes.cgroup-delegate` used to be a hard error — *"'cgroup-delegate' is
// reserved for the built-in cgroup delegate service"* — because the name could not be a
// loophole. It is one now (docs/design/loophole-activation.md OQ-A4/OQ-A6), shipped by
// the official pack of the same name, and the refusal would make the delegate's own
// switch UNWRITABLE: `enabled: true` under that key is the only way to turn `yolo-cglimit`
// back on after the default flipped.

// The switch has to validate, at BOTH scopes, and this is the assertion the deleted
// refusal would fail.
//
// Either scope, because `enabled` is either-scope by ruling (R5) and the delegate is not
// an exception: a workspace may switch on what the user already installed. The pack
// selection stays user-scope by construction (`packs` is read from the user file), which
// is what bounds it.
func TestTheCgroupDelegateSwitchIsWritable(t *testing.T) {
	known := fakeResolver{"cgroup-delegate": {Name: "cgroup-delegate"}}
	for _, tc := range []struct{ name, user, ws string }{
		{"user scope", `{"loopholes": {"cgroup-delegate": {"enabled": true}}}`, ""},
		{"workspace scope", "", `{"loopholes": {"cgroup-delegate": {"enabled": true}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs, _ := validateScoped(t, tc.user, tc.ws, known)
			if len(errs) != 0 {
				t.Errorf("errors = %v — this is the delegate's ONLY switch, so refusing it "+
					"leaves `yolo-cglimit` with no way back on after OQ-A4 flipped the default",
					errs)
			}
		})
	}
}

// AND THE REFUSAL IS GONE IN ITS OWN WORDS, not merely inert. A message still saying
// "reserved for the built-in cgroup delegate service" would be false twice over: there is
// no built-in, and the name is a pack's.
//
// Checked over the shape the old rule refused UNCONDITIONALLY — an entry under the name
// with no resolver at all, which is what a machine that has not selected the pack sees.
// That case still produces the ordinary unknown-loophole warning, and it must not produce
// a reservation error.
func TestTheCgroupDelegateNameIsNoLongerReserved(t *testing.T) {
	_, errs, warns := validateScoped(t,
		`{"loopholes": {"cgroup-delegate": {"enabled": true}}}`, "", nil)
	for _, msg := range append(append([]string{}, errs...), warns...) {
		if strings.Contains(msg, "reserved") {
			t.Errorf("a message still calls the name reserved: %q. The name belongs to the "+
				"official `cgroup-delegate` pack now, and yolo's own in-process delegate is "+
				"gated on THAT record", msg)
		}
	}
}

// An OVERRIDE still cannot redefine the daemon, and this is what replaced the name check
// rather than nothing replacing it. With the loophole known, `command` and `doctor_cmd`
// are refused by the general override rule — so a config cannot point the delegate's name
// at a program of its own while the pack is selected.
func TestTheCgroupDelegateCannotBeRedefinedByConfig(t *testing.T) {
	known := fakeResolver{"cgroup-delegate": {Name: "cgroup-delegate"}}
	_, errs, _ := validateScoped(t,
		`{"loopholes": {"cgroup-delegate": {"command": ["/bin/false"]}}}`, "", known)
	if len(containing(errs, "not overridable")) != 1 {
		t.Fatalf("errors = %v, want the override refusal — the delegate's behaviour is fixed "+
			"by its manifest, and a config that appeared to change it would change nothing",
			errs)
	}
}
