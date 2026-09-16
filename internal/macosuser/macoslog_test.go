package macosuser

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// macoslog_test.go pins the `macos_log` dial ACROSS THE PACKAGE BOUNDARY, which is the
// only place it was ever broken (docs/plans/setup-support-gaps.md F1). Every piece of the
// feature worked in isolation and had a test: the generator knew three modes, the plan
// carried the value to the bootstrap, the bootstrap installed the helper. The schema knew
// no such key, and an unknown top-level key is a FATAL pre-flight error — so the chain's
// end-to-end behaviour was "yolo-log tells you to write `macos_log`, and writing it stops
// the jail launching". No unit test could see that, because none of them crossed from the
// generated text into the validator that judges the config the text asks for.

// The remedy yolo PRINTS has to be a config yolo ACCEPTS. This is the F1 regression test:
// it fails with `config.macos_log: unknown key` against the schema as it stood before
// 2026-09-16.
func TestMacosLogRemedyIsAConfigThePreflightAccepts(t *testing.T) {
	off := MacosLogWrapperScript("off")
	if !contains(off, "macos_log") {
		t.Fatalf("the off-mode yolo-log stub no longer names the key that enables it; "+
			"this test is asserting nothing:\n%s", off)
	}

	// Every mode the stub could name, not just the "user" it spells out — the stub also
	// says "(or full)", and a vocabulary the validator half-accepts is the same defect
	// one value narrower.
	for _, mode := range config.MacosLogModes {
		cfg := jsonx.NewOrderedMap()
		cfg.Set("macos_log", mode)
		errs, warns := config.ValidateConfig(cfg, t.TempDir(), nil)
		for _, e := range errs {
			if strings.Contains(e, "macos_log") {
				t.Errorf("`\"macos_log\": %q` is refused by the pre-flight: %s\n"+
					"yolo-log prints this very setting as the way to enable itself, so a "+
					"refusal here walks the user into a jail that will not start.", mode, e)
			}
		}
		for _, w := range warns {
			if strings.Contains(w, "macos_log") {
				t.Errorf("`\"macos_log\": %q` earns a warning: %s", mode, w)
			}
		}
	}
}

// The other half of one vocabulary: every mode the schema accepts must generate a
// DIFFERENT helper from every other. Pairwise rather than "not the off one" because the
// generator fails in two directions and only this shape sees both — a mode missing from
// its map degrades to "off" (the user asked for logs and got the stub), while a mode in
// the map with no case of its own falls through to the "user" body (the user asked for
// something and got scoped log access they did not name). The second is the one a widened
// vocabulary produces, and it is the one that hands out MORE than was asked for.
func TestEveryAcceptedMacosLogModeGeneratesItsOwnHelper(t *testing.T) {
	seen := map[string]string{}
	for _, mode := range config.MacosLogModes {
		got := MacosLogWrapperScript(mode)
		if other, dup := seen[got]; dup {
			t.Errorf("modes %q and %q generate the SAME yolo-log helper — one of them is "+
				"a value the schema accepts and the generator has no case for, so it is "+
				"silently served as the other:\n%s", other, mode, got)
		}
		seen[got] = mode
	}
	// And nothing wider: a value outside the list still degrades to off, which is the
	// defence for a jail config snapshot written by a launcher older than the vocabulary.
	if MacosLogWrapperScript("verbose") != MacosLogWrapperScript("off") {
		t.Error("an unrecognised mode must degrade to off")
	}
}

// The middle link: the value reaches the native bootstrap, and an absent key resolves to
// the default there rather than to an empty string (an empty YOLO_DARWIN_MACOS_LOG makes
// `yolo internal darwin-bootstrap` generate the off stub anyway, so the failure would be
// invisible — hence the assertion on the literal "off").
func TestMacosLogReachesTheDarwinBootstrapEnv(t *testing.T) {
	plan := func(cfg *jsonx.OrderedMap) RunPlan {
		return BuildRunPlan("/Users/Shared/yolo/proj", cfg,
			[]string{"claude"}, []string{"claude"}, "/usr/local/bin/yolo", "", "",
			HostContext{}, jsonx.NewOrderedMap(), nil, nil)
	}
	for _, mode := range config.MacosLogModes {
		cfg := jsonx.NewOrderedMap()
		cfg.Set("macos_log", mode)
		p := plan(cfg)
		if !containsArg(p.BootstrapArgv, "YOLO_DARWIN_MACOS_LOG="+mode) {
			t.Errorf("the bootstrap argv does not carry YOLO_DARWIN_MACOS_LOG=%s:\n%v",
				mode, p.BootstrapArgv)
		}
	}
	if p := plan(jsonx.NewOrderedMap()); !containsArg(p.BootstrapArgv, "YOLO_DARWIN_MACOS_LOG=off") {
		t.Errorf("an absent macos_log must resolve to the off default on the argv:\n%v",
			p.BootstrapArgv)
	}
}
