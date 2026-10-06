package run

// sealnarrowing_test.go pins PPX-D39 (docs/design/patched-extensions.md; selectionNarrowed, seal.go)
// from Run's own entry point: a build jail's NARROWED selection is not refused by a gate asking
// whether another selected pack provides what one names, because the narrowing is what dropped that
// pack. Each fixture is a config the user's own launch accepts; its build jail, sealed to the pack
// that names the other one, must reach the runtime too. Red, case by case, if the skip in front of
// its gate is deleted. The first launch with patched extensions met the briefing case: pack matt's
// `agents: ["pi"]`, and every patched extension's build jail refused before its build line ran.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// narrowingHome writes a user config selecting agentpack, whose program makes `tool` an agent, and
// treepack, which contributes a patched extension listed in tool's settings and, beside it, extra
// (contributions appended to its list) and top (top-level manifest members, each ending in a
// comma). agentExtra is appended to agentpack's contributions, and files are written relative to
// the packs directory.
func narrowingHome(t *testing.T, extra, top, agentExtra string, files map[string]string) {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(packs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agentpack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"},
		{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}`+
		agentExtra+`]}`)
	write("treepack/pack.json", `{`+top+`"contributes":[{"kind":"files","into":"`+treeInto+`",
		"source":"git+https://example.invalid/tree-ext?ref=main","patches":"patches"},
		{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/`+treeInto+`"]}`+extra+`]}`)
	write("treepack/patches/0001-x.patch", "From 0123456789abcdef0123456789abcdef01234567 Mon Sep 17 00:00:00 2001\n"+
		"Subject: [PATCH] x\n\n---\n\nbase-commit: 0123456789abcdef0123456789abcdef01234567\n")
	for rel, body := range files {
		write(rel, body)
	}
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "agentpack")+`","name":"agentpack"},`+
		`{"source":"file://`+filepath.Join(packs, "treepack")+`","name":"treepack"}]`)
}

// narrowingRefusals are what a refusal from one of the skipped gates says, and what the skipped
// report says (reportUnmatchedAudiences): content addressed to an agent the seal does not carry.
var narrowingRefusals = []string{"which no pack in `packs` provides", "Refusing to launch", "no capability",
	"so this launch delivers it to no agent"}

func TestABuildJailsNarrowingIsNotRefusedForWhatItDropped(t *testing.T) {
	for _, tc := range []struct {
		name, extra, top, agentExtra string
		files                        map[string]string
	}{
		// THE ONE THE MAINTAINER MET: prose addressed to an agent the seal does not carry.
		{name: "a briefing `agents` selector",
			extra: `,{"kind":"briefing","agents":["tool"],"from":"briefing/tool.md"}`,
			files: map[string]string{"treepack/briefing/tool.md": "Prose for tool.\n"}},
		{name: "a skills `agents` selector",
			extra: `,{"kind":"skills","agents":["tool"],"from":"skills"}`,
			files: map[string]string{"treepack/skills/hello/SKILL.md": "---\nname: hello\ndescription: Says hello.\n---\nHello.\n"}},
		// A claim on a capability only the dropped pack's loophole serves.
		{name: "a `supersedes` claim",
			top:        `"supersedes":[{"capability":"tool-refresh","because":"the fixture refreshes tool itself"}],`,
			agentExtra: `,{"kind":"loophole","from":"loopholes/tool-broker"}`,
			files: map[string]string{"agentpack/loopholes/tool-broker/manifest.jsonc": `{"name":"tool-broker",` +
				`"description":"tool's refresher","transport":"none","default_enabled":true,"serves":["tool-refresh"]}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			narrowingHome(t, tc.extra, tc.top, tc.agentExtra, tc.files)
			// THE USER'S OWN LAUNCH accepts the config: the gate has nothing to refuse over the
			// whole selection, so a refusal below is the narrowing's alone.
			if argv, printed := fakePodmanLaunch(t, func(*Options) {}); argv == nil {
				t.Fatalf("the user's own launch of the fixture never reached the runtime:\n%s", printed)
			}
			// THE BUILD JAIL, as runCaptureJail makes it: sealed, told its tree, narrowed to the
			// contributing pack, and with no capture store.
			argv, printed := fakePodmanLaunch(t, func(o *Options) {
				o.Sealed, o.SealedTree, o.OnlyPacks = true, "tree-ext", []string{"treepack"}
				o.CapturesDir = func() string { return "" }
			})
			if argv == nil {
				t.Fatalf("the sealed build jail never reached the runtime:\n%s", printed)
			}
			for _, w := range narrowingRefusals {
				if strings.Contains(printed, w) {
					t.Errorf("the sealed build jail says %q:\n%s", w, printed)
				}
			}
		})
	}
}

// A FORK'S BUILD JAIL TOO (seal = [fork pack, base]): the fork pack addresses prose to an agent a
// third pack provides, which the seal does not carry.
func TestAForkBuildJailsNarrowingIsNotRefusedForWhatItDropped(t *testing.T) {
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	for rel, body := range map[string]string{
		"basepack/pack.json":  `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`,
		"otherpack/pack.json": `{"contributes":[{"kind":"program","bin":"other","via":"npm","package":"other"}]}`,
		"forkpack/pack.json": `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",` +
			`"source":"` + forkPinSource + `","build":"make install","produces":[".local/bin/tool"]},` +
			`{"kind":"briefing","agents":["other"],"from":"briefing/other.md"}]}`,
		"forkpack/briefing/other.md": "Prose for other.\n",
	} {
		p := filepath.Join(packs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "basepack")+`","name":"basepack"},`+
		`{"source":"file://`+filepath.Join(packs, "otherpack")+`","name":"otherpack"},`+
		`{"source":"file://`+filepath.Join(packs, "forkpack")+`","name":"forkpack"}]`)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Sealed, o.OnlyPacks = true, []string{"forkpack", "basepack"}
		o.CapturesDir = func() string { return "" }
	})
	if argv == nil {
		t.Fatalf("the fork's sealed build jail never reached the runtime:\n%s", printed)
	}
	if strings.Contains(printed, "which no pack in `packs` provides") {
		t.Errorf("the fork's sealed build jail refused the agent the seal dropped:\n%s", printed)
	}
}

// THE VIA-ROUTE GATE is skipped for a narrowed selection as well: what it protects is an agent's
// first request through the bridge, and a build jail hands its agents no profile at all
// (sealedChannel). The same config unnarrowed is refused (TestAViaWithNoRouteForItsAgentIsRefused).
func TestABuildJailsNarrowingIsNotRefusedForAViaRoute(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pa": {"provider": "anth", "via": "wire-bridge"}}}`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"pi": "pa"}, OnlyPacks: []string{"pi"},
		stagingCfg: viaStagingCfg(t, "anth", `{"anthropic": {"base_url": "https://anth.example"}}`)}
	if _, _, _, err := o.stagePacks("yolo-test-via-narrowed"); err != nil {
		t.Fatalf("a narrowed selection was refused by the via-route gate: %v", err)
	}
}
