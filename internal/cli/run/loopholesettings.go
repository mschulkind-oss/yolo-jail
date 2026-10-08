package run

import (
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// loopholesettings.go is the LAUNCH half of docs/reference/pack-system.md:
// internal/loopholedecl declared the keys, internal/config validated the values,
// and here core resolves them once and writes the file the daemon is handed.

type preparedLoopholeSettings struct {
	candidates []*loopholes.Loophole
	bytes      map[string][]byte
	values     map[string]*jsonx.OrderedMap
	checked    map[string]bool
}

// prepareLoopholeSettings resolves and validates the exact settings this launch intends to use.
// It has no publication side effects; its bytes can be carried across a keeper boundary and are
// published only after the launch has disclosed the host code it is about to run.
func (o *Options) prepareLoopholeSettings(discovered []*loopholes.Loophole, cfg *jsonx.OrderedMap) *preparedLoopholeSettings {
	plan := &preparedLoopholeSettings{
		bytes: make(map[string][]byte), values: make(map[string]*jsonx.OrderedMap), checked: make(map[string]bool),
	}
	loopCfg := cfgMap(cfg, "loopholes")
	for _, lp := range discovered {
		if len(lp.Settings) == 0 {
			continue
		}
		supplied := suppliedSettings(loopCfg, lp.Name)
		frozen, problems, err := loopholes.FrozenSettingsBytes(lp, supplied)
		if err != nil {
			o.pr(o.Stdout).print("[red]Could not resolve settings for loophole " + lp.Name +
				": " + err.Error() + " — its daemon will not start[/red]")
			o.startupRefusal = &hostStartupRefusal{name: lp.Name, class: "settings-resolution",
				reason: "The declared settings could not be resolved.",
				remedy: "Correct the host service settings and run `yolo check --no-build` again."}
			return nil
		}
		for _, problem := range problems {
			o.pr(o.Stdout).print("[yellow]Warning: " + problem + "[/yellow]")
		}
		values, _ := loopholes.ResolveSettings(lp, supplied)
		o.discloseLoopholeSettingsResolved(lp, values)
		checked := lp.HostDaemon != nil && len(lp.HostDaemon.SettingsCheck) > 0
		if checked {
			result := loopholes.RunSettingsCheck(lp, frozen)
			if result.Outcome != hostservice.CommandAccepted {
				class := string(result.Outcome)
				if result.Outcome == hostservice.CommandRefused {
					class = "configuration"
				}
				o.startupRefusal = &hostStartupRefusal{name: lp.Name, class: class,
					reason: result.Reason, remedy: result.Remedy}
				return nil
			}
		}
		plan.candidates = append(plan.candidates, lp)
		plan.bytes[lp.Name] = append([]byte(nil), frozen...)
		plan.values[lp.Name] = values
		plan.checked[lp.Name] = checked
	}
	return plan
}

// publishLoopholeSettings publishes a previously validated plan, without rereading config or the
// validator input. The settings disclosure already preceded the validator during preparation.
func (o *Options) publishLoopholeSettings(plan *preparedLoopholeSettings) {
	if plan == nil {
		return
	}
	o.settingsSnapshots = make(map[string]*ownedSettingsSnapshot)
	for _, lp := range plan.candidates {
		if plan.checked[lp.Name] && lp.HostDaemon != nil && lp.HostDaemon.Scope != loopholes.ScopeHost {
			path, cleanup, err := loopholes.WritePrivateSettingsSnapshot(lp, plan.bytes[lp.Name])
			if err != nil {
				o.cleanupSettingsSnapshots()
				o.startupRefusal = &hostStartupRefusal{name: lp.Name, class: "settings-publication",
					reason: "The validated settings snapshot could not be prepared for the daemon.",
					remedy: "Check host storage permissions and run `yolo check --no-build` again."}
				return
			}
			o.settingsSnapshots[lp.Name] = &ownedSettingsSnapshot{
				path: path, bytes: append([]byte(nil), plan.bytes[lp.Name]...), cleanup: cleanup,
			}
			continue
		}
		if plan.checked[lp.Name] {
			o.settingsSnapshots[lp.Name] = &ownedSettingsSnapshot{
				path: loopholes.SettingsFileFor(lp.Name), bytes: append([]byte(nil), plan.bytes[lp.Name]...),
			}
			continue
		}
		if err := loopholes.WriteSettingsBytes(loopholes.SettingsFileFor(lp.Name), plan.bytes[lp.Name]); err != nil {
			o.pr(o.Stdout).print("[red]Could not write settings for loophole " + lp.Name +
				": " + err.Error() + " — it will start with its declared defaults[/red]")
		}
	}
}

// writeLoopholeSettings is retained for direct lifecycle callers and tests: resolve once, then
// publish the exact bytes that were resolved.
func (o *Options) writeLoopholeSettings(discovered []*loopholes.Loophole, cfg *jsonx.OrderedMap) {
	plan := o.prepareLoopholeSettings(discovered, cfg)
	if o.startupRefusal != nil {
		return
	}
	o.publishLoopholeSettings(plan)
}

func (o *Options) discloseSettingsCheckHostExec(packs []*packload.Pack, set loopholes.Set,
	cfg *jsonx.OrderedMap, allow func(string) bool) {
	discovered := set.Enabled()
	kept := discovered[:0]
	for _, lp := range discovered {
		if allow(lp.Name) && len(lp.PlacementProblems(o.Workspace)) == 0 && lp.HostDaemon != nil &&
			len(lp.HostDaemon.SettingsCheck) > 0 {
			kept = append(kept, lp)
		}
	}
	order, _ := hostDaemonOrder(set, kept, cfg, allow)
	for _, name := range order {
		lp, ok := set.Lookup(name)
		if !ok {
			continue
		}
		pack := sourcePackName(packs, lp)
		if pack == "" {
			pack = "selected pack"
		}
		o.pr(o.Stderr).printf("[bold yellow]Running the settings validator from pack %q for host service %q on your machine[/bold yellow]", pack, name)
	}
}

func sourcePackName(packs []*packload.Pack, lp *loopholes.Loophole) string {
	if lp == nil {
		return ""
	}
	moduleDir := filepath.Clean(lp.Path)
	for _, pack := range packs {
		if pack == nil {
			continue
		}
		mods, _, _ := pack.LoopholeModules()
		for _, mod := range mods {
			if filepath.Clean(mod.Dir) == moduleDir {
				return pack.Name
			}
		}
	}
	return ""
}

func (o *Options) frozenLoopholeSettings() map[string][]byte {
	if o.settingsPlan == nil {
		return nil
	}
	out := make(map[string][]byte, len(o.settingsPlan.bytes))
	for name, b := range o.settingsPlan.bytes {
		out[name] = append([]byte(nil), b...)
	}
	return out
}

func (o *Options) cleanupSettingsSnapshots() {
	for name, snapshot := range o.settingsSnapshots {
		if snapshot != nil && snapshot.cleanup != nil {
			snapshot.cleanup()
		}
		delete(o.settingsSnapshots, name)
	}
}

// discloseLoopholeSettings prints `loophole <name>: <sentence>` for every bool setting
// whose declaration carries a `disclose` sentence and whose RESOLVED value is true.
func (o *Options) discloseLoopholeSettingsResolved(lp *loopholes.Loophole, values *jsonx.OrderedMap) {
	out := o.pr(o.Stderr)
	for _, decl := range lp.Settings {
		if decl.Disclose == "" {
			continue
		}
		if v, ok := values.Get(decl.Key); ok && v == true {
			out.print("[bold yellow]loophole " + lp.Name + ": " + decl.Disclose + "[/bold yellow]")
		}
	}
}

// suppliedSettings returns the `loopholes.<name>.settings` object from the merged config,
// or nil when nothing supplied any.
func suppliedSettings(loopCfg *jsonx.OrderedMap, name string) *jsonx.OrderedMap {
	if loopCfg == nil {
		return nil
	}
	entry := cfgMap(loopCfg, name)
	if entry == nil {
		return nil
	}
	return cfgMap(entry, "settings")
}
