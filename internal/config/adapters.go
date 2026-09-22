package config

// The adapters config key: docs/reference/protocol-resolution.md#the-adapters-address

// adapters.go implements the `adapters` config key — the one field of an adapter a user
// may override, which is the ADDRESS the conversion is served at.
//
// WHY ONLY THE ADDRESS. The pair is the declaration's identity and the pack's whole claim:
// a user who wanted a different conversion would be declaring an adapter, which is a pack's
// job. What they may need is a different PORT, and the reason is backend-shaped rather than
// hypothetical (§6): in a container `127.0.0.1` is the jail's own private loopback, so a
// collision is only possible with another baked service; on `macos-user` there is no
// container and no network namespace, so an adapter's ports are HOST ports and collide with
// whatever the user is already running.
//
// SCOPE, and it is the same security model `packs` and `profiles` run (OQ-CS5's reason,
// applied to a field that steers the same thing): USER-SCOPE ONLY, read from
// paths.UserConfigPath() DIRECTLY rather than from the merged config, so workspace scope is
// inexpressible by construction — and refused explicitly besides, because a reader who
// writes it in the workspace file must hear why rather than watch it do nothing. An address
// decides where an agent's inference goes, which is exactly what a committed,
// agent-editable file may not decide; it is the same line `providers.<name>.endpoints`
// draws (validateProviderAddressScope), for the same reason.
//
// This file LOWERS only. Which pairs exist is the packs' business, so a key naming a
// conversion nothing declares is inert rather than invalid — the open-vocabulary rule the
// protocol names themselves follow. What it checks is the shape an entry can be wrong in
// without that vocabulary.

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// adaptersKey is the top-level config key.
const adaptersKey = "adapters"

// LoadAdapterAddresses returns the user's adapter address overrides, keyed the way
// packload.AdapterKey spells a pair.
//
// It takes no merged config, for LoadProfiles' reason: reading the user file directly is
// what makes workspace scope inexpressible, and a caller that handed it the merged map
// would undo that guarantee one call site at a time.
//
// A malformed entry is a WARNING plus a skip, never a silently dropped key and never a hard
// error here — LoadProfiles' rule, and its argument: the hard error is validateAdapters'
// job, at `yolo check` and at every launch, where it can be fatal.
func LoadAdapterAddresses(warn Warn) (map[string]string, error) {
	if warn == nil {
		warn = func(string) {}
	}
	userPath := paths.UserConfigPath()
	userCfg, err := loadUserScopeConfig(userPath, userPath, true, warn)
	if err != nil {
		return nil, err
	}
	v, present := userCfg.Get(adaptersKey)
	if !present || v == nil {
		return nil, nil
	}
	out, problems := checkAdapters(v)
	for _, p := range problems {
		warn(p + " — entry skipped")
	}
	return out, nil
}

// checkAdapters lowers a raw `adapters` value into pair → address, returning the entries it
// could read and a problem string per entry it could not.
func checkAdapters(v any) (map[string]string, []string) {
	m, ok := asMap(v)
	if !ok {
		return nil, []string{"config." + adaptersKey +
			`: expected an object of "<from>-><to>" → {"address": "<url>"}`}
	}
	var problems []string
	out := map[string]string{}
	for _, key := range m.Keys() {
		path := "config." + adaptersKey + "." + key
		from, to, ok := strings.Cut(key, "->")
		// The KEY IS THE PAIR, because the pair is the adaptation's sole-owned identity —
		// the same thing a provider's entry is keyed by its name. A key that is not a pair
		// names no conversion at all, so it cannot be honored loosely.
		if !ok || strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
			problems = append(problems, path+
				`: the key is the conversion, spelled "<from>-><to>" (e.g. "openai->anthropic")`)
			continue
		}
		entryV, _ := m.Get(key)
		if entryV == nil {
			continue // a null drops the override, the convention every other entry has
		}
		entry, ok := asMap(entryV)
		if !ok {
			problems = append(problems, path+`: expected an object with an "address"`)
			continue
		}
		reportUnknownKeysTo(&problems, entry, set("address"), path)
		addrV, has := entry.Get("address")
		if !has || addrV == nil {
			problems = append(problems, path+`: needs an "address" — the URL the converted `+
				`wire is served at, which is the only field of an adapter a config may set`)
			continue
		}
		addr, ok := asStr(addrV)
		if !ok {
			problems = append(problems, path+".address: expected a string")
			continue
		}
		if problem := providerURLProblem(addr); problem != "" {
			problems = append(problems, path+".address: "+problem)
			continue
		}
		out[packload.AdapterKey(strings.TrimSpace(from), strings.TrimSpace(to))] = addr
	}
	if len(out) == 0 {
		return nil, problems
	}
	return out, problems
}

// validateAdapters is the `adapters` key's hard half: the lowering's problems as ERRORS,
// plus the workspace-scope refusal.
//
// It reads the USER config and the WORKSPACE config separately, never the merged map —
// validatePacks is the pattern and validateProfiles restates the reason: in the merged map
// an `adapters` key from either scope looks the same, and only the workspace one is wrong.
func validateAdapters(workspace string, errs *[]string) {
	userPath := paths.UserConfigPath()
	if userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {}); err == nil && userCfg != nil {
		if v, present := userCfg.Get(adaptersKey); present && v != nil {
			_, problems := checkAdapters(v)
			for _, p := range problems {
				add(errs, p)
			}
		}
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(adaptersKey); atWorkspace {
		add(errs, adapterScopeMessage())
	}
}

// adapterScopeMessage is the workspace-scope refusal, in providerAddressScopeMessage's
// shape and for its reason: what this key sets is an ADDRESS, and an address is where every
// prompt, every file the agent has read and every credential hydrated for that provider go.
func adapterScopeMessage() string {
	return "config." + adaptersKey + ": user-scope only — move it to " + paths.UserConfigPath() +
		". A workspace config travels with the repo and is agent-editable, and this key sets " +
		"the ADDRESS an adapted provider is reached at: every prompt, every file the agent " +
		"has read and every credential hydrated for that provider are sent there. The " +
		"adapter itself — which wire it converts into which — stays a pack's declaration; " +
		"this key changes only where it answers."
}

// reportUnknownKeysTo is reportUnknownKeys against a problem SLICE rather than the
// validator's error pointer, so the lowering can be shared by the warning path (LoadAdapterAddresses)
// and the error path (validateAdapters) without either inventing its own key census.
func reportUnknownKeysTo(problems *[]string, m *jsonx.OrderedMap, known map[string]struct{}, path string) {
	for _, k := range m.Keys() {
		if _, ok := known[k]; !ok {
			*problems = append(*problems, path+"."+k+": unknown key")
		}
	}
}
