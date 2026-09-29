package run

// THE DURABLE DIR's launch half (docs/design/durable-scratch-space.md §5.2, OQ-DS1): every
// fresh launch, on all three jail backends, makes `<workspace>/.yolo/durable` and — only when
// that worked — exports its in-jail path as $YOLO_DURABLE_DIR to every process of the jail.
// The briefing's storage-classes section leads with the same value, so the agent is told
// the path the variable holds and never one that does not exist.
//
// A FAILURE NEVER REFUSES A LAUNCH (DS-D2): a jail without durable space is the status quo
// the design improves on, so the launch continues, prints one line saying why, and the
// briefing says the same. An attach makes nothing: it shares the running jail, whose frozen
// environment already holds the variable its own launch exported, so the attach reads that.

import (
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// ensureDurableDir makes this fresh launch's durable dir and records the answer on o, where
// refreshJailBriefings and the backend's env emission both read it. rt decides the in-jail
// spelling: /workspace/.yolo/durable on the container backends (reached through the
// workspace bind, DS-D8), the real path on macos-user, which mounts nothing.
func (o *Options) ensureDurableDir(rt string, cfg *jsonx.OrderedMap) *jailcontent.DurableDir {
	d := &jailcontent.DurableDir{}
	o.durable = d
	// A --dry-run (macos-user's) describes the launch and creates nothing in the workspace:
	// it asks why the directory could not be made, and reports the path a real launch would.
	ensure := func(ws string) error { _, err := durable.Ensure(ws); return err }
	if o.DryRun {
		ensure = durable.Check
	}
	if reason := durableReadonlyReason(cfg, o.Workspace); reason != "" {
		d.Unavailable = reason
	} else if err := ensure(o.Workspace); err != nil {
		d.Unavailable = durable.Reason(err, o.Workspace)
	} else if slices.Contains(paths.NativeRuntimes, rt) { // parity: HonoredBy — macos-user reaches the same directory at the workspace's real path, which is where its agent runs
		d.Path = durable.HostPath(o.Workspace)
	} else {
		d.Path = durable.ContainerJailPath
	}
	if d.Path == "" {
		o.pr(o.Stdout).print("[yellow]" + durable.UnavailableLine(d.Unavailable) + "[/yellow]")
		return d
	}
	d.Caveat = o.durableCaveat()
	return d
}

// durableDirFromLaunchEnv is an attach's answer: the variable the running jail's launch
// exported, from its frozen environment. A jail started without one has none this session,
// and the attach says why as precisely as it can WITHOUT creating anything: the causes a
// fresh launch would still hit today (a covering `workspace_readonly` entry, a link or a
// non-directory at `.yolo` or `durable`), else neutral words, since the launch may have been
// older than the durable dir or have failed for a reason that is gone.
func durableDirFromLaunchEnv(envLines []string, cfg *jsonx.OrderedMap, workspace string) *jailcontent.DurableDir {
	if p := envLineValue(envLines, durable.EnvVar); p != "" {
		return &jailcontent.DurableDir{Path: p}
	}
	if reason := durableReadonlyReason(cfg, workspace); reason != "" {
		return &jailcontent.DurableDir{Unavailable: reason}
	}
	if err := durable.Check(workspace); err != nil {
		return &jailcontent.DurableDir{Unavailable: durable.Reason(err, workspace)}
	}
	return &jailcontent.DurableDir{Unavailable: "this jail was started without one (by an older " +
		"launcher, or a launch that could not make it); a fresh launch tries again"}
}

// attachDurableDir is an attach's durable dir: the one the running jail's launch exported
// (durableDirFromLaunchEnv), with the fresh launch's caveat. The attach rewrites the briefing
// the jail reads, so without the caveat a nested jail's second terminal put "yolo never
// deletes it" back over the first launch's truthful lifetime (DS-D32). The caveat needs no
// frozen state: it is a fact about the launcher's frame and the workspace, the same for an
// attach as for the launch, since only the enclosing jail's own processes reach its nested one.
func (o *Options) attachDurableDir(envLines []string, cfg *jsonx.OrderedMap) *jailcontent.DurableDir {
	d := durableDirFromLaunchEnv(envLines, cfg, o.Workspace)
	if d.Path != "" {
		d.Caveat = o.durableCaveat()
	}
	return d
}

// durableReadonlyReason is the words for a `workspace_readonly` entry covering the durable
// dir, or "" when none does.
func durableReadonlyReason(cfg *jsonx.OrderedMap, workspace string) string {
	if entry, covered := durableCoveredByReadonly(cfg, workspace); covered {
		return "the `workspace_readonly` entry `" + entry + "` makes it read-only"
	}
	return ""
}

// durableJailPath is the value the launch exports, or "" for none.
func (o *Options) durableJailPath() string {
	if o.durable == nil {
		return ""
	}
	return o.durable.Path
}

// durableCoveredByReadonly reports the `workspace_readonly` entry that covers the durable
// dir, if one does: the launcher then does not make it, since the jail could not write it
// (§5.6). Lexical, never resolving a link: `.yolo` is jail-writable.
func durableCoveredByReadonly(cfg *jsonx.OrderedMap, workspace string) (string, bool) {
	dir := durable.HostPath(workspace)
	for _, rel := range cfgStrList(cfg, "workspace_readonly") {
		if isUnderOrEqual(dir, filepath.Clean(filepath.Join(workspace, rel))) {
			return rel, true
		}
	}
	return "", false
}

// durableCaveat is the sentence a nested jail needs: a workspace inside the ENCLOSING jail's
// per-launch set (a launch from /tmp/yolo-nested, the repository's own verification loop)
// gets a durable dir that lasts only as long as that jail (§5.6).
func (o *Options) durableCaveat() string {
	if !o.inJail() {
		return ""
	}
	for _, s := range prune.ScratchSlots {
		if isUnderOrEqual(o.Workspace, s.Dest) {
			return "This workspace is itself inside the enclosing jail's per-launch `" + s.Dest +
				"`, so this directory lasts only as long as that jail."
		}
	}
	return ""
}
