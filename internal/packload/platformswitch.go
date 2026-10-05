package packload

// platformswitch.go is PP-D1 (docs/design/providers-and-profiles-redesign.md, a maintainer
// ruling of 2026-09-29): yolo obeys a switch a user wrote in their own agent config that puts the
// agent on a provider platform by itself — CLAUDE_CODE_USE_BEDROCK in ~/.claude/settings.json —
// and when the agent's selected provider is not of that platform, so the credential gate sends
// it none of that platform's credentials, the launch prints ONE line naming the conflict and both
// fixes: select a provider of the platform, or remove the key.
//
// YOLO DELETES NOTHING IT DID NOT WRITE. The maintainer: "there could be any uncountable number of
// environment variables … that we can just like never know about", which rules deletion out; the
// line exists because the gate is yolo's, so the failure it causes is yolo's to name.
//
// CORE NAMES NO AGENT, FILE OR VARIABLE (OQ-CS8): the switch is the agent pack's declaration
// (packdecl.PlatformSwitch — packs/claude declares its settings file's env key), read here from
// the user's own copy of that surface, the one a `readsHost` surface composes as its host layer.
//
// A SWITCH yolo WROTE IS NOT THE USER'S, and the line says so. `yolo host apply` writes claude's
// switch into the real file while the HOST selection is Bedrock (providers.md#pv-d8), so a launch
// selecting another provider meets a key yolo put there. The host's computed-leaf record
// (render.Target.LeafRecordPath, HC-D25) names every leaf that apply wrote with its value; a
// switch holding the recorded value is yolo's, and its line names `yolo host apply`, which clears
// it once claude's host selection leaves the platform, instead of telling the user to remove a
// key of theirs. The caller answers from the record (render.HostLeafWrote): this package reads
// no render target. Only where a host apply renders (host_management "own"): under "none" the
// apply refuses, so the line names removing the key by hand, or `--revert` (HostApplyRenders).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// LeafWrote answers whether yolo's own host apply wrote value at pointer in surface ("agent/name")
// and it is unchanged since (render.HostLeafWrote). Nil answers no for every switch.
type LeafWrote func(surface, pointer string, value any) bool

// PlatformSwitchConflict is one agent whose own config switches it onto a platform that its
// selection does not serve.
type PlatformSwitchConflict struct {
	Agent    string
	Platform string
	// File is the switch's file as its surface spells it ("~/.claude/settings.json"), Key the
	// key the switch's pointer ends in.
	File, Key string
	// Profile is a declared profile, routed through no via service, over a provider of the
	// platform — the `-p` the line offers; "" when none is declared.
	Profile string
	// WrittenByYolo is whether the switch holds the value `yolo host apply` recorded writing
	// there (the host's computed-leaf record), so the key is yolo's and not the user's.
	WrittenByYolo bool
	// HostApplyRenders is whether a `yolo host apply` renders in this home: host_management is
	// "own". The caller sets it from the user config (config.HostManagementMode), which this
	// package does not read. False under "none" — the unset key since the `assert` retirement
	// (OQ-CO14) — where the apply refuses, so a switch yolo wrote is named for removal by hand or
	// by `--revert`, never by an apply that would refuse at every launch.
	HostApplyRenders bool
}

// Line is the one line a launch prints for the conflict.
func (c PlatformSwitchConflict) Line() string {
	fix := "select a provider of that platform for " + c.Agent
	if c.Profile != "" {
		fix = "select one (-p " + c.Profile + ")"
	}
	if c.WrittenByYolo && !c.HostApplyRenders {
		// NOT "set own and apply": own's first apply ADOPTS the file as it finds it, so a switch
		// the retired `assert` wrote becomes a captured key of the user's rather than going.
		// `--revert` runs under "none" and takes out the leaf the record names.
		return fmt.Sprintf("%s: %s sets %s, which `yolo host apply` wrote there for %s's host "+
			"selection, and it puts %s on its own %s client, but no %s provider is selected for %s, "+
			"so yolo delivers it none of that platform's credentials: %s, or remove %s from %s by "+
			"hand, since no host apply renders here while host_management is not \"own\" (`yolo host "+
			"apply --revert` lists every key yolo wrote, this one included, and takes them out with "+
			"--assert).", c.Agent, c.File, c.Key, c.Agent, c.Agent, strconv.Quote(c.Platform),
			strconv.Quote(c.Platform), c.Agent, fix, c.Key, c.File)
	}
	if c.WrittenByYolo {
		return fmt.Sprintf("%s: %s sets %s, which `yolo host apply` wrote there for %s's host "+
			"selection, and it puts %s on its own %s client, but no %s provider is selected for %s, "+
			"so yolo delivers it none of that platform's credentials: %s, or run `yolo host apply` "+
			"with %s on a provider of another platform, which removes it.", c.Agent, c.File, c.Key,
			c.Agent, c.Agent, strconv.Quote(c.Platform), strconv.Quote(c.Platform), c.Agent, fix, c.Agent)
	}
	return fmt.Sprintf("%s: %s sets %s, which puts %s on its own %s client, but no %s provider is "+
		"selected for %s, so yolo delivers it none of that platform's credentials: %s, or remove %s "+
		"from %s (yolo leaves it alone).", c.Agent, c.File, c.Key, c.Agent, strconv.Quote(c.Platform),
		strconv.Quote(c.Platform), c.Agent, fix, c.Key, c.File)
}

// PlatformSwitchConflicts is every conflict PP-D1 names for this launch, in pack order: for each
// agent a selected pack installs — only `agent` when it is non-empty, the one process the host
// notch composes — each platform switch its program declares, read from the user's own copy of
// the switch's surface under home, that is ON while the agent's selected provider (sel, the
// gate's own selection) is not of the switch's platform. A surface this pack does not declare, or
// one with no host layer, has no user copy to read and is skipped, as is a file that is absent or
// not JSON: the agent reports its own config errors. wrote says which switches yolo's own host
// apply wrote (WrittenByYolo); nil says none did. HostApplyRenders is left false, for the caller
// to set on each from the user config.
func PlatformSwitchConflicts(packs []*Pack, sel GateSelection, resolved map[string]ResolvedProfile,
	providers *jsonx.OrderedMap, home, agent string, wrote LeafWrote) []PlatformSwitchConflict {
	var out []PlatformSwitchConflict
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, bin := range p.InstallBins() {
			if agent != "" && bin != agent {
				continue
			}
			for _, sw := range p.Decl.PlatformSwitches(bin) {
				if sel.Platforms[bin] == sw.Platform {
					continue // served: the selected provider is of the platform
				}
				file, value, on := switchIsOn(p, sw, home)
				if !on {
					continue
				}
				out = append(out, PlatformSwitchConflict{Agent: bin, Platform: sw.Platform,
					File: file, Key: sw.Key(), Profile: nativeProfileOn(resolved, providers, sw.Platform),
					WrittenByYolo: wrote != nil && wrote(sw.Surface, sw.Pointer, value)})
			}
		}
	}
	return out
}

// switchIsOn reads sw from the user's own copy of its surface under home, returning the file as
// the surface spells it, the value the switch holds there, and whether the switch is on.
func switchIsOn(p *Pack, sw packdecl.PlatformSwitch, home string) (string, any, bool) {
	surfaces, _ := p.Surfaces()
	for _, s := range surfaces {
		if s.Key().String() != sw.Surface || !s.ReadsHost {
			continue
		}
		rel, ok := strings.CutPrefix(s.Path, "~/")
		if !ok {
			return s.Path, nil, false
		}
		data, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			return s.Path, nil, false
		}
		doc, err := jsonx.Decode(data)
		if err != nil {
			return s.Path, nil, false
		}
		steps, err := jsonptr.Parse(sw.Pointer)
		if err != nil {
			return s.Path, nil, false
		}
		v := any(doc)
		for _, step := range steps {
			m, ok := v.(*jsonx.OrderedMap)
			if !ok {
				return s.Path, nil, false
			}
			if v, ok = m.Get(step); !ok {
				return s.Path, nil, false
			}
		}
		return s.Path, v, switchTruthy(v)
	}
	return sw.Surface, nil, false
}

// switchTruthy is the common environment-flag convention a PlatformSwitch documents: JSON true,
// the number 1, or a string reading 1, true, yes or on in any case.
func switchTruthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	default:
		switch strings.ToLower(strings.TrimSpace(fmt.Sprint(v))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// nativeProfileOn is the first declared profile, by name, over a provider of platform that routes
// through no via service — the selection that serves the switch — or "" when none is declared.
func nativeProfileOn(resolved map[string]ResolvedProfile, providers *jsonx.OrderedMap, platform string) string {
	for _, name := range ProfilesOnPlatform(resolved, providers, platform) {
		if resolved[name].Via == "" {
			return name
		}
	}
	return ""
}
