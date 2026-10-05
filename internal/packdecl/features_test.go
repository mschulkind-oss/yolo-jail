package packdecl

// features_test.go pins the list `yolo features` prints (docs/design/patched-forks.md PF-D63): the
// kinds and vias derived from their closed sets, and each named capability backed by code that
// reads it, so a name cannot outlive the capability it promises.

import (
	"regexp"
	"strings"
	"testing"
)

func TestFeaturesListEveryKindAndViaAndTheNamedCapabilities(t *testing.T) {
	got := map[string]bool{}
	var names []string
	for _, f := range Features() {
		if got[f.Name] {
			t.Errorf("feature %q is listed twice", f.Name)
		}
		got[f.Name] = true
		names = append(names, f.Name)
		if f.Summary == "" {
			t.Errorf("feature %q has no summary", f.Name)
		}
	}
	for _, k := range KnownKinds() {
		if !got["kind:"+string(k)] {
			t.Errorf("kind %q is not listed", k)
		}
	}
	for _, v := range KnownVias() {
		if !got["via:"+v] {
			t.Errorf("via %q is not listed", v)
		}
	}
	for _, name := range []string{"patch-series", "patched-extensions", "skips-unreadable-contributions"} {
		if !got[name] {
			t.Errorf("%q is not listed", name)
		}
	}
	if !sortedStrings(names) {
		t.Errorf("features are not sorted: %v", names)
	}
}

// A name is a stable token a script tests for, so it is kebab-case, optionally behind one family
// prefix.
func TestFeatureNamesAreStableTokens(t *testing.T) {
	token := regexp.MustCompile(`^((kind|via):)?[a-z0-9]+(-[a-z0-9]+)*$`)
	for _, f := range Features() {
		if !token.MatchString(f.Name) {
			t.Errorf("feature name %q is not a kebab-case token", f.Name)
		}
	}
}

// EVERY NAMED CAPABILITY IS ONE THIS BUILD READS. A case per name; a name with no case fails, so
// adding one to namedFeatures means proving it here in the same commit (PF-D63's rule).
func TestEveryNamedFeatureIsReadable(t *testing.T) {
	for _, f := range namedFeatures {
		switch f.Name {
		case "patch-series":
			_, problems := Decode([]byte(`{"name":"pi-mine","contributes":[{"kind":"program","bin":"pi",
			  "via":"source","fork_of":"pi","source":"git+https://example.com/pi?ref=main","build":"make",
			  "produces":[".local/bin/pi"],"patches":"patches","follow":"head"}]}`))
			if len(problems) != 0 {
				t.Errorf("patch-series: a patched fork does not decode: %v", problems)
			}
		case "patched-extensions":
			_, problems := Decode([]byte(`{"name":"x","contributes":[{"kind":"files",
			  "into":".pi/agent/yolo-patched/x","source":"git+https://example.com/x?ref=main",
			  "patches":"patches"}]}`))
			if len(problems) != 0 {
				t.Errorf("patched-extensions: a patched extension does not decode: %v", problems)
			}
		case "skips-unreadable-contributions":
			_, problems, skipped := DecodeForUse([]byte(`{"name":"x","contributes":[
			  {"kind":"env","vars":{"A":"1"},"newer":1}]}`))
			if len(problems) != 0 || len(skipped) != 1 {
				t.Errorf("skips-unreadable-contributions: problems=%v skipped=%v", problems, skipped)
			}
		default:
			t.Errorf("named feature %q has no case proving this build reads it", f.Name)
		}
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if strings.Compare(s[i-1], s[i]) > 0 {
			return false
		}
	}
	return true
}
