package config

import (
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// RenderedHostFilePaths returns the home-relative jail destinations this config's
// `host_files` would actually RENDER into a jail — the set a pack's `overridden_by`
// `host_file` declaration is compared against (packload.EnvOverrideRefusal).
//
// It answers "what will the jail find at this path", which is narrower than "what does the
// config mention", on purpose: the refusal the answer feeds has no escape hatch and must
// never fire on a false positive (docs/design/sso-backed-bedrock.md, OQ-SSO8), and an entry
// that renders nothing overrides nothing. So it reads the entries exactly as the launch
// does (LoadHostFiles — the source-bearing half from the USER config alone, which is also
// why a workspace-scope source-bearing entry is never counted), and then keeps an entry
// only when it lands something:
//
//   - a SOURCE-LESS entry always does (inline `content`, or `managed`/`defaults` — an
//     entry with none of those is refused by checkHostFiles);
//   - a SOURCE-BEARING entry does when its host source exists as the kind the entry
//     declares (a directory for a trailing-`/` entry, a file otherwise), which is the
//     mount side's own test (run.hostUserFileArgs skips a missing bind source, since podman
//     dies on one) — or when it carries `managed`/`defaults`, which the surface falls back
//     to when the source is absent;
//   - and a source-bearing DIRECTORY entry does only when dirsDeliver says this launch's
//     backend delivers directories at all. Two do not, whether or not the source exists:
//     macos-user copies host bytes and never copies a tree (run.buildMacosCtxTree reports
//     the entry as undelivered), and Apple Container below the read-only-bind floor
//     declines the bind (run.hostUserFileArgs). Which backend a launch runs on is the
//     caller's to know, so the caller answers (run.hostFileDirsDeliver for the launch;
//     `yolo check` answers for the platform it runs on).
//
// The source probe is safe in a jail, unlike LoadHostFiles' probeSource: that flag turns a
// file/directory MISMATCH into a problem, and a host path absent from the jail's mount
// namespace would make every nested run fail. Here an absent source is simply not counted
// — the launch in this namespace would mount nothing from it either — so the worst a probe
// can do is return fewer paths, which costs a false negative and never a refusal.
//
// A user config that cannot be read returns nil, which is also what the launch then renders
// (it warns "no host files staged" and stages none), and "I could not look" must never read
// as a grant.
func RenderedHostFilePaths(merged *jsonx.OrderedMap, dirsDeliver bool) []string {
	entries, err := LoadHostFiles(merged, nil, false)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.rendersContent(dirsDeliver) {
			out = append(out, e.Path)
		}
	}
	return out
}

// rendersContent reports whether this entry lands anything in the jail. See
// RenderedHostFilePaths for the rule and why each arm is there.
func (e HostFileEntry) rendersContent(dirsDeliver bool) bool {
	if !e.SourceBearing() || e.Managed != nil || e.Defaults != nil {
		return true
	}
	if e.IsDir && !dirsDeliver {
		return false
	}
	st, err := os.Stat(e.Source)
	if err != nil {
		return false
	}
	return st.IsDir() == e.IsDir
}
