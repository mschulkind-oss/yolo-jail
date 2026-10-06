package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// provisioners.go is the `provisioners` key: the USER'S PROVISIONER ORDER, which re-ranks the
// recipes the selected packs declare for a binary (docs/design/provisioner-sets.md §8.2, and the
// grain OQ-PS7 ruled 2026-10-05 as PS-D9).
//
//	"provisioners": {
//	  "host": ["brew", "pack"],                         // the order, at the host
//	  "per_package": { "claude": { "host": ["brew"] } } // one binary's own order, rarely needed
//	}
//
// # The two grains, and which one is advertised
//
// OQ-PS7's answer: ONE ordered list per environment is the surface a user sets, and a per-package
// override exists but is not advertised — the maintainer's "don't overwhelm the user with package
// choices" and his "claude from brew" both hold that way. So `host` is the list the user guide
// shows, and `per_package` is spelled in `yolo config-ref` alone. Per environment, never global:
// a jail's order is not a host's (P2), so the list is keyed by environment even while `host` is
// the one environment this yolo resolves (PS-D11).
//
// # What a name means, and what the order does
//
// A name is a provisioner (depcheck.Provisioners): `pack`, the declaring pack's own recipe (its
// npm package, or its vendor's installer), or a package manager. The order RE-RANKS: the first
// name that is on the launch PATH and has a recipe for the binary wins, and yolo's default order
// (the pack's recipe, then the detected manager) follows the list, so a name that covers nothing
// on this machine is skipped rather than fatal, and naming brew does not leave an npm-only program
// with no remedy. A per-package order replaces the environment's list for that binary.
//
// # Why it is read from the USER config directly
//
// It decides which command yolo offers and runs to install a program on the user's machine, and a
// workspace config is agent-editable, so a workspace spelling is refused by validateProvisioners
// and never read, `host_floor`'s construction.
const provisionersKey = "provisioners"

// ProvisionerEnvHost is the one environment a provisioner order is read for today: the host notch,
// whose dependency report, install offer and agent floor resolve through it.
const ProvisionerEnvHost = "host"

// provisionersPerPackage is the sub-key holding the per-package orders.
const provisionersPerPackage = "per_package"

// ProvisionerOrder is the user's provisioner order for bin in env: its per-package order when the
// user config writes one, else the environment's list, with every name validation refuses left
// out. nil when neither is written, which is yolo's default order alone.
func ProvisionerOrder(env, bin string) []string {
	return provisionerOrder(UserScopeConfigOrEmpty(), env, bin)
}

// provisionerOrder is the reading, split from the user-config lookup so the validator and the
// tests exercise one implementation.
func provisionerOrder(cfg *jsonx.OrderedMap, env, bin string) []string {
	v, present := cfg.Get(provisionersKey)
	root, ok := v.(*jsonx.OrderedMap)
	if !present || !ok {
		return nil
	}
	if pp, ok := root.Get(provisionersPerPackage); ok {
		if byBin, ok := pp.(*jsonx.OrderedMap); ok {
			if one, ok := byBin.Get(bin); ok {
				if envs, ok := one.(*jsonx.OrderedMap); ok {
					if list, ok := envs.Get(env); ok {
						return provisionerNames(list)
					}
				}
			}
		}
	}
	list, _ := root.Get(env)
	return provisionerNames(list)
}

// provisionerNames is a written order's names that are provisioners, in written order, each once.
func provisionerNames(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range list {
		s, ok := e.(string)
		if !ok || !depcheck.IsProvisioner(s) || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// validateProvisioners checks the `provisioners` key: its shape, every environment and every
// provisioner name in it, and each per-package order against the recipes the selected packs ship
// (PS-D3). User scope only. It warns where an order ranks nix above the pack's own recipe, which
// does not reach a program the host floor holds yet, and only for a program a selected pack ships a
// nix recipe for (nixAbovePack).
func validateProvisioners(config *jsonx.OrderedMap, workspace string, errs, warns *[]string) {
	v, present := config.Get(provisionersKey)
	if !present {
		return
	}
	if v != nil {
		var reqs, floorReqs map[string][]depcheck.Requirement
		complete := true
		if hasPerPackage(v) || namesNix(v) {
			reqs, floorReqs, complete = selectedDepRequirements(config)
		}
		for _, prob := range provisionersProblems(v, reqs, complete) {
			add(errs, "config."+provisionersKey+prob)
		}
		for _, w := range nixAbovePack(config, floorReqs) {
			add(warns, "config."+provisionersKey+w.at+": "+fmt.Sprintf(provisionersNixWarning,
				strings.Join(w.bins, ", ")))
		}
	}
	wsCfg, err := LoadWorkspaceConfig(workspace, false, func(string) {})
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(provisionersKey); atWorkspace && wsValue != nil {
		add(errs, "config."+provisionersKey+": user-scope only — it decides which command yolo "+
			"offers and runs to install a program on your machine, so it is read from "+
			paths.UserConfigPath()+" and a workspace value has no effect. Move it there, or remove it.")
	}
}

// provisionersNixWarning is what an order ranking nix above the pack's own recipe for a floor
// program is told, %s being those programs. PS-D2 decided that an agent the user's order gives to
// nix is built, pinned by yolo's own flake.lock, into its host floor entry rather than put in a
// `nix profile`; that build is not written, so the floor keeps such an agent on its own recipe
// (hostfloor.Outranking), and nix supplies only what the floor does not hold.
const provisionersNixWarning = "nix is ranked above the pack's own recipe for %s, but an agent yolo's host " +
	"floor holds does not come from nix yet (docs/design/provisioner-sets.md PS-D2): the floor keeps " +
	"its own copy, and nix supplies only the programs it does not hold, such as a pack's `requires`. " +
	"To run it from nix now, leave its pack out of the floor (`host_floor`) and install it yourself"

// nixWarning is one order provisionersNixWarning is about: its path below the key, and the floor
// programs it would give to nix.
type nixWarning struct {
	at   string
	bins []string
}

// nixAbovePack is every order in cfg's `provisioners` that would give a FLOOR PROGRAM to nix —
// one a selected pack installs with its own recipe (an npm package or a vendor installer) and
// also ships a nix recipe for — where the floor in fact keeps it (hostfloor.Outranking). An order
// does that when nix comes before `pack` in it, or it names nix and leaves `pack` to yolo's
// default, which comes after the list. reqs is what the selected packs declare per binary
// (selectedDepRequirements). Nothing else is warned about: a `requires` has no floor entry, so
// nix supplying it is the order working, and no shipped agent pack ships a nix recipe, so an
// order naming nix changes nothing for them.
func nixAbovePack(cfg *jsonx.OrderedMap, reqs map[string][]depcheck.Requirement) []nixWarning {
	var bins []string
	for bin, declared := range reqs {
		for _, r := range declared {
			if r.SelfInstall != "" && depcheck.HasRecipe(r, NixProvisioner) {
				bins = append(bins, bin)
				break
			}
		}
	}
	sort.Strings(bins)
	var out []nixWarning
	byAt := map[string]int{}
	for _, bin := range bins {
		if !nixBeforePack(provisionerOrder(cfg, ProvisionerEnvHost, bin)) {
			continue
		}
		at := "." + ProvisionerEnvHost
		if hasPerPackageOrder(cfg, ProvisionerEnvHost, bin) {
			at = "." + provisionersPerPackage + "." + bin + "." + ProvisionerEnvHost
		}
		if i, ok := byAt[at]; ok {
			out[i].bins = append(out[i].bins, bin)
			continue
		}
		byAt[at] = len(out)
		out = append(out, nixWarning{at: at, bins: []string{bin}})
	}
	return out
}

// NixProvisioner is the provisioner name of the user's nix.
const NixProvisioner = "nix"

// nixBeforePack reports whether order names nix ahead of `pack`, or names nix and not `pack`.
func nixBeforePack(order []string) bool {
	for _, name := range order {
		switch name {
		case depcheck.Pack:
			return false
		case NixProvisioner:
			return true
		}
	}
	return false
}

// namesNix reports whether any order in v names nix, the one case nixAbovePack needs the selected
// packs resolved for.
func namesNix(v any) bool {
	root, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	has := func(list any) bool {
		for _, name := range provisionerNames(list) {
			if name == NixProvisioner {
				return true
			}
		}
		return false
	}
	if has(mapGet(root, ProvisionerEnvHost)) {
		return true
	}
	if byBin, ok := mapGet(root, provisionersPerPackage).(*jsonx.OrderedMap); ok {
		for _, bin := range byBin.Keys() {
			if envs, ok := mapGet(byBin, bin).(*jsonx.OrderedMap); ok && has(mapGet(envs, ProvisionerEnvHost)) {
				return true
			}
		}
	}
	return false
}

// hasPerPackageOrder reports whether cfg writes bin's own order for env, the one provisionerOrder
// then reads in place of the environment's list.
func hasPerPackageOrder(cfg *jsonx.OrderedMap, env, bin string) bool {
	root, ok := mapGet(cfg, provisionersKey).(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	byBin, ok := mapGet(root, provisionersPerPackage).(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	envs, ok := mapGet(byBin, bin).(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	_, ok = envs.Get(env)
	return ok
}

// mapGet is m's value at k, nil when absent.
func mapGet(m *jsonx.OrderedMap, k string) any {
	v, _ := m.Get(k)
	return v
}

// hasPerPackage reports whether v writes any per-package order, the one part whose check needs
// the selected packs resolved.
func hasPerPackage(v any) bool {
	root, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	_, ok = root.Get(provisionersPerPackage)
	return ok
}

// selectedDepRequirements returns all selected requirements for recipe validation,
// the requirements whose packs remain in the effective host floor for warnings,
// and whether the whole selection resolved. Exclusion must not erase a binary's
// declared recipes: those are still available when the floor does not own it.
func selectedDepRequirements(cfg *jsonx.OrderedMap) (map[string][]depcheck.Requirement, map[string][]depcheck.Requirement, bool) {
	packs, complete := resolveSelectedPacks()
	var floorPacks []*packload.Pack
	wire := hostFloorWire(cfg)
	for _, p := range packs {
		if allowed, _ := PackPolicyDecision(wire, p.Name); allowed {
			floorPacks = append(floorPacks, p)
		}
	}
	return depRequirementsOf(packs), depRequirementsOf(floorPacks), complete
}

func depRequirementsOf(packs []*packload.Pack) map[string][]depcheck.Requirement {
	out := map[string][]depcheck.Requirement{}
	for _, p := range packs {
		for _, d := range p.Decl.DepRequirements() {
			out[d.Bin] = append(out[d.Bin], depcheck.Requirement{Bin: d.Bin, Hints: d.Hints,
				SelfInstall: d.SelfInstall})
		}
	}
	return out
}

// provisionersProblems is every problem in a `provisioners` value, each starting with the path
// below the key it is about. reqs is what the selected packs declare per binary (nil when the
// value writes no per-package order), and complete whether the whole selection resolved: a binary
// no RESOLVED pack declares is refused only when nothing was left unresolved, since a pack this
// read could not resolve may be the one declaring it (resolveSelectedPacks says why partial is
// the right answer for a check made before staging).
func provisionersProblems(v any, reqs map[string][]depcheck.Requirement, complete bool) []string {
	root, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return []string{": expected an object of environments, e.g. {\"host\": [\"brew\", \"nix\"]} (got " +
			pyReprValue(v) + ")"}
	}
	var out []string
	for _, k := range root.Keys() {
		val, _ := root.Get(k)
		if k == provisionersPerPackage {
			out = append(out, perPackageProblems(val, reqs, complete)...)
			continue
		}
		if prob := provisionerEnvProblem(k); prob != "" {
			out = append(out, "."+k+": "+prob)
			continue
		}
		out = append(out, orderProblems("."+k, val)...)
	}
	return out
}

// provisionerEnvProblem is why env is not an environment an order may be written for, "" when it
// is one.
func provisionerEnvProblem(env string) string {
	switch env {
	case ProvisionerEnvHost:
		return ""
	case "jail":
		return "no order is read in a jail: its agents come from the pack's own launcher and capture " +
			"store, never another provisioner (docs/design/provisioner-sets.md OQ-PS6), so this list " +
			"would change nothing. \"host\" is the one environment this yolo resolves; remove it"
	}
	return fmt.Sprintf("unknown environment %s; the one this yolo resolves is \"host\" (or %q, for one "+
		"binary's own order)", pyReprValue(env), provisionersPerPackage)
}

// orderProblems checks one written order: a list of provisioner names, each once.
func orderProblems(at string, v any) []string {
	list, ok := v.([]any)
	if !ok {
		return []string{at + ": expected a list of provisioners, most preferred first, e.g. " +
			"[\"brew\", \"nix\"] (got " + pyReprValue(v) + ")"}
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range list {
		s, ok := e.(string)
		switch {
		case !ok || !depcheck.IsProvisioner(s):
			out = append(out, fmt.Sprintf("%s: %s is not a provisioner; the names are %s", at,
				pyReprValue(e), strings.Join(depcheck.Provisioners(), ", ")))
		case seen[s]:
			out = append(out, fmt.Sprintf("%s: %q is named twice; an order names each provisioner once", at, s))
		}
		if ok {
			seen[s] = true
		}
	}
	return out
}

// perPackageProblems checks `per_package`: an object of binaries, each an object of environments,
// each an order naming only provisioners some selected pack ships a recipe for (PS-D3).
func perPackageProblems(v any, reqs map[string][]depcheck.Requirement, complete bool) []string {
	at := "." + provisionersPerPackage
	byBin, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return []string{at + ": expected an object of binaries, e.g. {\"claude\": {\"host\": [\"brew\"]}} (got " +
			pyReprValue(v) + ")"}
	}
	var out []string
	for _, bin := range byBin.Keys() {
		binAt := at + "." + bin
		val, _ := byBin.Get(bin)
		envs, ok := val.(*jsonx.OrderedMap)
		if !ok {
			out = append(out, binAt+": expected an object of environments, e.g. {\"host\": [\"brew\"]} (got "+
				pyReprValue(val)+")")
			continue
		}
		if !packdecl.ValidBinName(bin) {
			out = append(out, fmt.Sprintf("%s: %q is not a binary name", binAt, bin))
			continue
		}
		declared := reqs[bin]
		if len(declared) == 0 && complete {
			out = append(out, fmt.Sprintf("%s: no selected pack declares %s as a `program` or a `requires`, "+
				"so this order would rank nothing. Check the binary's name (`yolo check-deps` lists every "+
				"one), or remove it", binAt, bin))
			continue
		}
		for _, env := range envs.Keys() {
			list, _ := envs.Get(env)
			envAt := binAt + "." + env
			if prob := provisionerEnvProblem(env); prob != "" {
				out = append(out, envAt+": "+prob)
				continue
			}
			if probs := orderProblems(envAt, list); len(probs) > 0 {
				out = append(out, probs...)
				continue
			}
			if len(declared) == 0 {
				continue // declared by a pack this read could not resolve, if by any
			}
			for _, name := range provisionerNames(list) {
				if !anyRecipe(declared, name) {
					out = append(out, unshippedRecipeProblem(envAt, bin, name, declared))
				}
			}
		}
	}
	return out
}

// anyRecipe reports whether any declaration of a binary carries a recipe for provisioner.
func anyRecipe(declared []depcheck.Requirement, provisioner string) bool {
	for _, r := range declared {
		if depcheck.HasRecipe(r, provisioner) {
			return true
		}
	}
	return false
}

// unshippedRecipeProblem is PS-D3's refusal: the order names a provisioner no selected pack ships
// a recipe for, so yolo would have no command to run. It names the recipes that exist and the
// route that does: leave the program out of the floor, install it another way, and `yolo host`
// runs the copy on the launch PATH (OQ-HE11).
func unshippedRecipeProblem(at, bin, name string, declared []depcheck.Requirement) string {
	var have []string
	for _, p := range depcheck.Provisioners() {
		if anyRecipe(declared, p) {
			have = append(have, p)
		}
	}
	sort.Strings(have)
	shipped := "none"
	if len(have) > 0 {
		shipped = strings.Join(have, ", ")
	}
	return fmt.Sprintf("%s: no selected pack declares how %s installs %s (its recipes: %s), and an "+
		"order only re-ranks the ones packs ship. To use your own install, leave it out of the floor "+
		"(`host_floor`), install it yourself, and `yolo host` runs the copy on your PATH; or remove %q",
		at, name, bin, shipped, name)
}
