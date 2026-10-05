package cli

// hostconfigkeys_test.go pins the report half of the config-key census
// (docs/design/declaration-parity.md OQ-DP5's second half, DP-B31) through the real
// `yolo host apply`: every key the user config declares that render's census says the host
// leaves undone is named on the notch line, once, and no key the host honors is. The census's
// own drift gate (every key classified, every entry with a reason) is render's
// configkeys_test.go; this file is the call site, so deleting inertConfigKeys from either
// branch of applyHostSurveyed fails it.

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// undoneKeySamples is a value that DECLARES something for every key the host census leaves
// undone. It must cover the census: TestHostApplyNamesEveryConfigKeyTheHostLeavesUndone fails
// when a key is classified not-applicable or unbuilt and has no sample here, so the next such
// key is driven through the report rather than assumed to reach it.
var undoneKeySamples = map[string]string{
	"brokered":           `{"github": {"workspaces": {"~/code/app": {"repos": ["org/lib"]}}}}`,
	"cache_relocations":  `{"npm": "/srv/cache/npm"}`,
	"devices":            `[{"usb": "0403:6001"}]`,
	"ephemeral_storage":  `"tmpfs"`,
	"gpu":                `{"enabled": true}`,
	"kvm":                `true`,
	"macos_log":          `"user"`,
	"mcp_presets":        `["sequential-thinking"]`,
	"mise_tools":         `{"node": "22"}`,
	"mounts":             `["~/data"]`,
	"network":            `{"mode": "bridge", "ports": ["3000:3000"]}`,
	"packages":           `["ripgrep"]`,
	"per_side_paths":     `[".cargo"]`,
	"programs":           `{"autoprune": true}`,
	"resources":          `{"memory": "4g"}`,
	"workspace_readonly": `["src"]`,
	"writable_home_dirs": `[".pi-lens"]`,
}

// undoneHostKeys is every live key the host census leaves undone, sorted.
func undoneHostKeys() []string {
	var out []string
	for _, k := range config.TopLevelConfigKeys() {
		if d, _ := render.HostFields().ConfigKey(k); d.LeftUndone() {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func TestHostApplyNamesEveryConfigKeyTheHostLeavesUndone(t *testing.T) {
	keys := undoneHostKeys()
	if len(keys) == 0 {
		t.Fatal("fixture bug: the host census leaves no key undone, so there is nothing to name")
	}
	var body []string
	for _, k := range keys {
		v, ok := undoneKeySamples[k]
		if !ok {
			t.Fatalf("the host census leaves %q undone and undoneKeySamples has no value for it: "+
				"add one that declares something, so the report is shown naming it", k)
		}
		body = append(body, `"`+k+`": `+v)
	}
	// Keys the host HONORS ride along, so the test also fails if the report names a key the
	// census says some host verb acts on: among them the three whose host readers are the newest
	// (`yolo host --`'s capability gate, the host notch's timing, and its blocked tools, HE-D11).
	honored := []string{"loopholes", "update_check", "required_capabilities", "perf_logging", "security"}
	body = append(body, `"loopholes": {}`, `"update_check": false`,
		`"required_capabilities": ["code_editing"]`, `"perf_logging": true`,
		`"security": {"blocked_tools": [{"name": "curl", "message": "no"}]}`)
	cfg := `{"packs": ["pi"], ` + strings.Join(body, ", ") + `}`

	for _, verbose := range []bool{false, true} {
		for _, packs := range []string{`["pi"]`, `[]`} {
			name := "packs " + packs
			if verbose {
				name += " verbose"
			}
			t.Run(name, func(t *testing.T) {
				hostComputedHome(t, strings.Replace(cfg, `["pi"]`, packs, 1))
				// The report's own verbosity gate (reportVerbose) reads the variable alone.
				if verbose {
					t.Setenv("YOLO_VERBOSE", "1")
				} else {
					t.Setenv("YOLO_VERBOSE", "")
				}
				rc, report := hostApplyRun(t, false)
				if rc != 0 {
					t.Fatalf("dry run rc=%d\n%s", rc, report)
				}
				line := configKeyNotchLine(report)
				if line == "" {
					t.Fatalf("no notch line names config keys:\n%s", report)
				}
				for _, k := range keys {
					if n := countWord(line, k); n != 1 {
						t.Errorf("%q appears %d times on the notch line, want exactly 1: %q", k, n, line)
					}
				}
				for _, k := range honored {
					if countWord(line, k) != 0 {
						t.Errorf("%q is honored at the host (the census names a reader), and the "+
							"notch line names it as undone: %q", k, line)
					}
				}
			})
		}
	}
}

// A key present with a value that declares nothing is not named: `"mounts": []` asks the
// host for nothing, so it leaves nothing undone.
func TestHostApplyDoesNotNameAKeyThatDeclaresNothing(t *testing.T) {
	hostComputedHome(t, `{"packs": ["pi"], "mounts": [], "kvm": false, "resources": {},
		"mise_tools": {"node": "22"}}`)
	t.Setenv("YOLO_VERBOSE", "")
	rc, report := hostApplyRun(t, false)
	if rc != 0 {
		t.Fatalf("dry run rc=%d\n%s", rc, report)
	}
	line := configKeyNotchLine(report)
	if countWord(line, "mise_tools") != 1 {
		t.Fatalf("mise_tools declares a tool and is not named once: %q\n%s", line, report)
	}
	for _, k := range []string{"mounts", "kvm", "resources"} {
		if countWord(line, k) != 0 {
			t.Errorf("%q declares nothing and is named as undone: %q", k, line)
		}
	}
}

// configKeyNotchLine is the report line naming config keys the host leaves undone: the one
// default-view notch line, or --verbose's config line.
func configKeyNotchLine(report string) string {
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(l, "config that does not apply at the host notch:") ||
			strings.Contains(l, "does not apply at the host:") {
			return l
		}
	}
	return ""
}

// countWord counts word in line as a whole name, so `mounts` is not found inside another key.
func countWord(line, word string) int {
	n := 0
	for rest := line; ; {
		i := strings.Index(rest, word)
		if i < 0 {
			return n
		}
		before := i == 0 || !isNameByte(rest[i-1])
		after := i+len(word) == len(rest) || !isNameByte(rest[i+len(word)])
		if before && after {
			n++
		}
		rest = rest[i+len(word):]
	}
}

func isNameByte(b byte) bool {
	return b == '_' || b == '-' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// AN INLINE LOOPHOLE IS NAMED BY ENTRY. The `loopholes` key is honored at the host (a pack
// loophole's doorway, its settings), so the key census above cannot name an entry the host does
// nothing with: an inline loophole — a `loopholes.<name>` entry with a `command` and no
// manifest, a host daemon whose only client is a jail. MEASURED 2026-10-04: an enabled one was
// named nowhere in `yolo host apply`'s report. Through both branches of applyHostSurveyed (an
// empty `packs`, which returns early, and a selected pack) and both postures, so deleting
// either call site fails a subtest.
func TestHostApplyNamesAnInlineLoopholeByEntry(t *testing.T) {
	const cfg = `{"packs": %s, "loopholes": {
		"mydaemon": {"command": ["mydaemon", "--socket", "{socket}"]},
		"sleeping": {"command": ["sleeper"], "enabled": false},
		"openai-auth-broker": {"enabled": true},
		"switch-only": {"enabled": true}}}`
	for _, packs := range []string{`[]`, `["pi"]`} {
		for _, write := range []bool{false, true} {
			name := "packs " + packs
			if write {
				name += " assert"
			}
			t.Run(name, func(t *testing.T) {
				hostComputedHome(t, strings.Replace(cfg, "%s", packs, 1))
				t.Setenv("YOLO_VERBOSE", "")
				rc, report := hostApplyRun(t, write)
				if rc != 0 {
					t.Fatalf("host apply rc=%d\n%s", rc, report)
				}
				line := configKeyNotchLine(report)
				if n := strings.Count(line, "inline loophole (mydaemon)"); n != 1 {
					t.Errorf("the enabled inline loophole is named %d times on the notch line, "+
						"want once: %q\n%s", n, line, report)
				}
				// A disabled entry runs nowhere; an entry without a command is a switch (an
				// override of a pack's loophole, or of one nothing ships), and runs no daemon.
				for _, notNamed := range []string{"sleeping", "openai-auth-broker", "switch-only"} {
					if countWord(line, notNamed) != 0 {
						t.Errorf("%q is not an enabled inline loophole and is named: %q", notNamed, line)
					}
				}
				// The key itself stays honored: only the entry is named.
				if strings.Contains(line, "does not apply at the host: loopholes") ||
					strings.Contains(line, ", loopholes,") {
					t.Errorf("the honored `loopholes` key is named as undone: %q", line)
				}
			})
		}
	}
	// --verbose says why, once.
	hostComputedHome(t, strings.Replace(cfg, "%s", `["pi"]`, 1))
	t.Setenv("YOLO_VERBOSE", "1")
	_, report := hostApplyRun(t, false)
	if !strings.Contains(report, "an inline loophole's only client is a jail") {
		t.Errorf("--verbose does not say why an inline loophole does not apply:\n%s", report)
	}
}
