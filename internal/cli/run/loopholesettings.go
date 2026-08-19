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
// REQUEST. That is what made the retired `host_processes.visible` editable without a
// restart — a real affordance, and indistinguishable from the hole: the same
// property let an AGENT widen its own allowlist mid-session, with no launch and
// therefore no approval gate, while the config diff was not in that causal path at
// all.
//
// # There is no legacy fold-in any more (2026-08-18)
//
// This file briefly carried one: the retired top-level `host_processes` block was
// merged into the settings supplied to the `host-processes` loophole, per key, so
// the key kept WORKING while its replacement landed. It was deleted in the step that
// moved the loophole into a pack, which is the step it was always scheduled for.
// The key is now a REFUSAL (config.validateHostProcessesRetired), which is the only
// alternative to honoring it that does not silently deny a capability someone asked
// for.
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
		_, problems, err := loopholes.WriteSettings(lp, suppliedSettings(loopCfg, lp.Name))
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
