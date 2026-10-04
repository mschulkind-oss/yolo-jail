package cli

// hostapplyinjail_test.go pins the in-jail refusal of a HOST apply (hostapplyinjail.go) at both of
// its call sites: `yolo host apply` (hostApply) and `yolo apply` at the host notch (applyMain),
// every spelling of each. Run with the jail marker set, each refuses, prints nothing on stdout and
// leaves the home byte for byte as it was; with the marker unset the same apply writes. Deleting
// either call site fails the rows that reach it.

import (
	"bytes"
	"strings"
	"testing"
)

// hostApplySpellings are every way to ask for a host apply, each as the front door hands it on.
var hostApplySpellings = []struct {
	name string
	cfg  string // extra user-config keys
	run  func(stdout, stderr *bytes.Buffer) int
}{
	{"host apply", "", func(o, e *bytes.Buffer) int { return hostMain([]string{"apply"}, o, e, false, nil) }},
	{"host apply --assert", "", func(o, e *bytes.Buffer) int {
		return hostMain([]string{"apply", "--assert"}, o, e, false, strings.NewReader("y\n"))
	}},
	{"host apply --revert", "", func(o, e *bytes.Buffer) int {
		return hostMain([]string{"apply", "--revert"}, o, e, false, nil)
	}},
	{"host apply --revert --assert", "", func(o, e *bytes.Buffer) int {
		return hostMain([]string{"apply", "--revert", "--assert"}, o, e, false, nil)
	}},
	{"host apply --format json", "", func(o, e *bytes.Buffer) int {
		return hostMain([]string{"apply", "--format", "json"}, o, e, false, nil)
	}},
	{"host apply --timing", "", func(o, e *bytes.Buffer) int {
		return hostMain([]string{"apply", "--timing"}, o, e, false, nil)
	}},
	{"apply --at host", "", func(o, e *bytes.Buffer) int { return applyMain([]string{"--at", "host"}, o, e, false, nil) }},
	{"apply --at host --assert", "", func(o, e *bytes.Buffer) int {
		return applyMain([]string{"--at", "host", "--assert"}, o, e, false, strings.NewReader("y\n"))
	}},
	{"apply --at host --revert --assert", "", func(o, e *bytes.Buffer) int {
		return applyMain([]string{"--at", "host", "--revert", "--assert"}, o, e, false, nil)
	}},
	{"apply under confinement: host", `, "confinement": "host"`, func(o, e *bytes.Buffer) int {
		return applyMain(nil, o, e, false, nil)
	}},
	{"apply --assert under confinement: host", `, "confinement": "host"`, func(o, e *bytes.Buffer) int {
		return applyMain([]string{"--assert"}, o, e, false, strings.NewReader("y\n"))
	}},
}

func TestAHostApplyRefusesInsideAJail(t *testing.T) {
	for _, sp := range hostApplySpellings {
		t.Run(sp.name, func(t *testing.T) {
			home := hostComputedHome(t, `{"packs": ["pi"]`+sp.cfg+`}`)
			t.Setenv("YOLO_VERSION", "0.0.0-in-a-jail")
			before := hashTree(t, home)
			var out, errw bytes.Buffer
			rc := sp.run(&out, &errw)
			if rc == 0 {
				t.Errorf("rc = 0, want a refusal\n%s", errw.String())
			}
			if out.Len() != 0 {
				t.Errorf("stdout is not empty:\n%s", out.String())
			}
			for _, w := range []string{"refusing — this process is inside a jail", home,
				"`yolo host apply --assert` in a terminal on the host", "Nothing was written."} {
				if !strings.Contains(errw.String(), w) {
					t.Errorf("the refusal lacks %q:\n%s", w, errw.String())
				}
			}
			if hashTree(t, home) != before {
				t.Errorf("a refused host apply wrote into the jail's home:\n%s", errw.String())
			}
		})
	}
}

// HELP STILL ANSWERS IN A JAIL: the refusal sits after the parse.
func TestHostApplyHelpAnswersInsideAJail(t *testing.T) {
	hostComputedHome(t, `{"packs": ["pi"]}`)
	t.Setenv("YOLO_VERSION", "0.0.0-in-a-jail")
	for _, argv := range [][]string{{"apply", "--help"}, {"apply", "-h"}} {
		var out, errw bytes.Buffer
		if rc := hostMain(argv, &out, &errw, false, nil); rc != 0 || !strings.Contains(out.String(), "yolo host") {
			t.Errorf("`yolo host %s` in a jail: rc=%d\nstdout:\n%s\nstderr:\n%s", strings.Join(argv, " "),
				rc, out.String(), errw.String())
		}
	}
}

// THE CONTROL: with no jail marker the same writing applies write, at both call sites, so the
// refusal above is the marker's doing and not a fixture that never writes.
func TestAHostApplyStillWritesOnTheHost(t *testing.T) {
	for _, sp := range hostApplySpellings {
		if sp.name != "host apply --assert" && sp.name != "apply --at host --assert" {
			continue
		}
		t.Run(sp.name, func(t *testing.T) {
			home := hostComputedHome(t, `{"packs": ["pi"]}`)
			before := hashTree(t, home)
			var out, errw bytes.Buffer
			if rc := sp.run(&out, &errw); rc != 0 {
				t.Fatalf("rc = %d\n%s%s", rc, out.String(), errw.String())
			}
			if hashTree(t, home) == before {
				t.Errorf("the apply wrote nothing on the host:\n%s", out.String())
			}
			if strings.Contains(errw.String(), "inside a jail") {
				t.Errorf("the host apply refused as if in a jail:\n%s", errw.String())
			}
		})
	}
}
