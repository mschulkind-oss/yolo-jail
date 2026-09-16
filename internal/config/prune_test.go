package config

// The `prune` block's validator, which did not exist until 2026-09-16.
//
// The key was in knownTopLevelConfigKeys and nothing looked inside it, so
// `prune: "hello"` validated, `prune: {"warn_threshold": 40}` validated, and neither
// did anything — `yolo check` reads `warn_threshold_gb` and only that. It also had no
// config-ref section, and the coverage test that should have caught THAT was satisfied
// by the substring "prune" inside `programs.autoprune`, so the two halves of the gap
// held each other up. G24 in docs/plans/setup-support-gaps.md.
//
// The threshold's lower bound is the interesting rule: checkDiskUsage ignores a value
// that is not `> 0` and falls back to 15 GiB, which is right at the read site and is
// exactly why a refusal belongs here. Someone writing 0 means "never warn me", which
// this key cannot express — silently warning them at 15 GiB instead is the surprise.

import (
	"strings"
	"testing"
)

// validatePruneErrs returns just the config.prune errors for a config carrying only
// that block.
func validatePruneErrs(t *testing.T, body string) []string {
	t.Helper()
	errs, _ := ValidateConfig(decode(t, `{"prune": `+body+`}`), t.TempDir(), nil)
	var out []string
	for _, e := range errs {
		if strings.HasPrefix(e, "config.prune") {
			out = append(out, e)
		}
	}
	return out
}

func TestValidatePruneAcceptsTheOneKeyItHas(t *testing.T) {
	// int, float and the absent block: every shape checkDiskUsage's numberFloat reads.
	for _, body := range []string{`{}`, `{"warn_threshold_gb": 40}`, `{"warn_threshold_gb": 12.5}`} {
		if errs := validatePruneErrs(t, body); len(errs) > 0 {
			t.Errorf("prune %s was refused: %v", body, errs)
		}
	}
}

func TestValidatePruneRefusesANonObject(t *testing.T) {
	for _, body := range []string{`"hello"`, `40`, `[]`, `true`} {
		errs := validatePruneErrs(t, body)
		if len(errs) == 0 {
			t.Errorf("prune %s validated; before 2026-09-16 every one of these did, "+
				"because the block had no validator at all", body)
			continue
		}
		if !strings.Contains(errs[0], "expected an object") {
			t.Errorf("prune %s: %q does not say what shape was expected", body, errs[0])
		}
	}
}

// The census. A misspelled sub-key is the failure mode this block is most exposed to,
// because `yolo prune`'s real knobs are FLAGS (--image-cache-keep, --cache-age,
// --nix-gc-max) and a reader who assumes the block mirrors them gets silence.
func TestValidatePruneNamesAnUnknownSubKey(t *testing.T) {
	for _, body := range []string{
		`{"warn_threshold": 40}`,    // the plausible typo: no _gb
		`{"warn_threshold_GB": 40}`, // case matters
		`{"image_cache_keep": 3}`,   // a flag mistaken for a config key
	} {
		errs := validatePruneErrs(t, body)
		if len(errs) == 0 {
			t.Errorf("prune %s validated, so the typo is silence", body)
			continue
		}
		if !strings.Contains(errs[0], "unknown key") {
			t.Errorf("prune %s: %q does not name it as an unknown key", body, errs[0])
		}
	}
}

func TestValidatePruneRefusesAThresholdTheReaderWouldIgnore(t *testing.T) {
	// Each of these reaches checkDiskUsage and loses to its `f > 0` guard, so the user
	// silently gets the 15 GiB default instead of what they wrote.
	for _, body := range []string{
		`{"warn_threshold_gb": 0}`,
		`{"warn_threshold_gb": -5}`,
		`{"warn_threshold_gb": "40"}`, // numberFloat does not accept a string
		`{"warn_threshold_gb": true}`, // nor a bool
		`{"warn_threshold_gb": []}`,
	} {
		errs := validatePruneErrs(t, body)
		if len(errs) == 0 {
			t.Errorf("prune %s validated, and the reader then ignores it — the exact "+
				"silent drop this validator exists to end", body)
			continue
		}
		if !strings.Contains(errs[0], "positive number") {
			t.Errorf("prune %s: %q does not say a positive number is wanted", body, errs[0])
		}
	}
}

// The wiring, separately from the rules: a validator nothing calls is the shape this
// repo has shipped five times (AGENTS.md, "a test that pins the CALLEE while the CALL
// SITE is unpinned is not a test"). Deleting the validatePrune line from
// ValidateConfig's list has to fail something, and this is it.
func TestValidatePruneIsReachedFromValidateConfig(t *testing.T) {
	errs, _ := ValidateConfig(decode(t, `{"prune": "hello"}`), t.TempDir(), nil)
	found := false
	for _, e := range errs {
		if strings.HasPrefix(e, "config.prune") {
			found = true
		}
	}
	if !found {
		t.Fatalf("ValidateConfig accepted a string `prune` block, so validatePrune is "+
			"not on its call list; errors were %v", errs)
	}
}
