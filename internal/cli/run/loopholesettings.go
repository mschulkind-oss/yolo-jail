package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// loopholesettings.go is the LAUNCH half of docs/design/pack-config-keys.md:
// internal/loopholedecl declared the keys, internal/config validated the values,
// and here core resolves them once and writes the file the daemon is handed.
//
// # Once, at launch, is the whole point (OQ-K3)
//
// The host-processes daemon used to re-read the raw workspace config on EVERY
// REQUEST. That is what made `host_processes.visible` editable without a restart —
// a real affordance, and indistinguishable from the hole: the same property let an
// AGENT widen its own allowlist mid-session, with no launch and therefore no
// approval gate, while the config diff was not in that causal path at all.
//
// Resolving here freezes it. Changing what a loophole may do now needs a restart,
// which is exactly where the config-approval gate lives — and that gate is a control
// rather than a courtesy only because the approval snapshot moved to host-side state
// the jail never mounts, and a non-interactive launch stopped auto-accepting.
//
// # Not gated on the pack origin gate, deliberately
//
// The file is written by yolo, from values yolo validated, into yolo's own state
// dir. It crosses nothing: an unapproved pack's daemon is never spawned, so its
// settings file is inert. The origin gate governs what REACHES the host or the jail
// (RuntimeArgsFor, ManifestHostDaemonSpecs, RunDoctorChecks), and adding a fourth
// face to it here would imply this write is a crossing, which would be the wrong
// thing to teach the next reader.

// writeLoopholeSettings resolves and writes the settings file for every enabled
// loophole that declares settings, so the argv about to be spawned names a file
// that exists and holds this launch's values.
//
// Problems are PRINTED, never fatal. Every one of them is something ValidateConfig
// already refuses host-side; the ones that can still arrive here are the in-jail
// downgrades, where refusing would break every nested launch over the live-mounted
// workspace file. The declaration wins in each case (ResolveSettings keeps the
// declared default), so a printed problem always describes a value that did NOT
// reach the daemon.
func (o *Options) writeLoopholeSettings(discovered []*loopholes.Loophole, cfg *jsonx.OrderedMap) {
	loopCfg := cfgMap(cfg, "loopholes")
	for _, lp := range discovered {
		if len(lp.Settings) == 0 {
			continue
		}
		supplied := suppliedSettings(loopCfg, lp.Name)
		supplied = withLegacyHostProcessesSettings(lp.Name, supplied, cfg, o)
		_, problems, err := loopholes.WriteSettings(lp, supplied)
		for _, prob := range problems {
			o.pr(o.Stdout).print("[yellow]Warning: " + prob + "[/yellow]")
		}
		if err != nil {
			// Named rather than swallowed: the daemon is about to be spawned with a
			// --settings path, and a missing file is the difference between "the
			// allowlist is empty" and "the allowlist could not be written". A daemon
			// that reads an absent file falls back to the type zeros, which is the
			// fail-closed direction — but silently, which is what this line prevents.
			o.pr(o.Stdout).print("[red]Could not write settings for loophole " + lp.Name +
				": " + err.Error() + " — it will start with its declared defaults[/red]")
		}
	}
}

// suppliedSettings returns the `loopholes.<name>.settings` object from the merged
// config, or nil when nothing supplied any.
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

// ---------------------------------------------------------------------------
// TEMPORARY: the top-level `host_processes` key, honored at the point of READ.
// ---------------------------------------------------------------------------

// legacyHostProcessesLoophole is the one loophole whose settings used to live in a
// top-level config key of their own.
const legacyHostProcessesLoophole = "host-processes"

// withLegacyHostProcessesSettings folds the RETIRED top-level `host_processes`
// block into the settings supplied to the `host-processes` loophole.
//
// # Why this exists, and when it goes
//
// `host_processes.visible` and `host_processes.fields` are becoming
// `loopholes.host-processes.settings.{visible,fields}`. The old key must keep
// WORKING through the migration — deleting it before its loophole is a pack would
// strand every user who has one — so it is honored here, where the value is READ.
// A validator-only alias would make `yolo check` go green while the daemon honored
// the old spelling forever, which is the failure docs/design/pack-config-keys.md §5
// names explicitly. The user-facing message naming the replacement is emitted by
// internal/config's validateHostProcesses, at every launch.
//
// Delete this function together with the top-level key, in the step that turns
// `host-processes` into a pack.
//
// # The new spelling wins, per key
//
// Not "whole block wins": a config mid-migration can name `visible` under the new
// spelling and still carry an old `fields`, and the surprising answer would be for
// the untouched key to vanish. So the legacy block fills in only the keys the new
// spelling did not supply.
func withLegacyHostProcessesSettings(
	name string, supplied *jsonx.OrderedMap, cfg *jsonx.OrderedMap, o *Options,
) *jsonx.OrderedMap {
	if name != legacyHostProcessesLoophole {
		return supplied
	}
	legacy := cfgMap(cfg, "host_processes")
	if legacy == nil || legacy.Len() == 0 {
		return supplied
	}
	out := jsonx.NewOrderedMap()
	if supplied != nil {
		for _, k := range supplied.Keys() {
			v, _ := supplied.Get(k)
			out.Set(k, v)
		}
	}
	carried := []string{}
	for _, k := range legacy.Keys() {
		if _, already := out.Get(k); already {
			continue
		}
		v, _ := legacy.Get(k)
		out.Set(k, v)
		carried = append(carried, k)
	}
	if len(carried) > 0 && o != nil {
		o.pr(o.Stdout).print("[dim]Using the retired top-level 'host_processes' key for " +
			joinComma(carried) + " — write them under " +
			"\"loopholes\": {\"host-processes\": {\"settings\": {…}}} instead.[/dim]")
	}
	return out
}
