package config

// mountsrequestbind.go is what the config-change gate approves for the `mounts` a WORKSPACE
// config declares, read-only and writable alike, and what the launch binds for them
// (docs/reference/context-mounts.md, oq-cx-symlink; RC-D24 for the writable request).
//
// NO MOUNTS IN PLACES THE AGENT CAN WRITE (mountPathRefusal). A source whose WRITTEN path ("~"
// expanded, made absolute, lexically cleaned, no link followed) is inside the workspace, or
// inside any other mount, is refused, read-only or writable: the jail already sees what is there,
// and a link there is the agent's to repoint at ~/.ssh before the next launch. That is the whole
// rule about links; no other link is refused.
//
// THE APPROVAL IS OF THE RESOLVED DESTINATION. A link anywhere else is followed, and is safe once
// the human approves where it lands: the approval record's config part carries every workspace
// mount's source as it resolved when the gate read it (mountRecordKey), the prompt shows that
// destination beside the written path, a retarget changes the record's bytes and asks again, and
// the launch binds the approved destination, refusing when the written path no longer resolves
// to it (WorkspaceMount.Drift).
//
// THE BIND IS OF THE GATE'S READ (WW-P2's rule for broker scope, applied to mounts). The gate's
// read is the one the human approved, and CheckWorkspaceMounts runs every refusal on exactly that
// read before the question is asked.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// mountRecordKey is the approval record's entry for the workspace mounts' resolved sources. It is
// not a config key (a space and parentheses keep it from being one), and it is present only when
// the workspace config declares a mount, so every other record's bytes are unchanged.
const mountRecordKey = "(mount sources, resolved)"

// WorkspaceMount is one `mounts` element of the workspace config as the config-change gate read
// it.
type WorkspaceMount struct {
	// Mount is the element as written.
	Mount ContextMount
	// Resolved is its source with "~" expanded, made absolute and every symbolic link resolved,
	// when the gate read it: what the human approves and what the launch binds.
	Resolved string
}

// writtenMountHost is host as written, made a path: "~" expanded, made absolute against the
// current directory (where the launch resolves a relative source), lexically cleaned, no link
// followed.
func writtenMountHost(host string) string {
	p := expandUser(host)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// resolveMountHost is host as the gate resolves it: writtenMountHost with every link in the
// longest existing prefix resolved (a source not there yet is judged by where it would be).
func resolveMountHost(host string) string {
	return resolveExistingPrefix(writtenMountHost(host))
}

// Dest is the jail path the mount lands at.
func (r WorkspaceMount) Dest() string { return r.Mount.DestFor(r.Resolved) }

// Line is the mount as the gate shows it: the resolved source and the jail path, and the path as
// written when it differs. Every part is made terminal-safe; the agent wrote them.
func (r WorkspaceMount) Line() string {
	s := termsafe.Visible(r.Resolved) + " → " + termsafe.Visible(r.Dest())
	if writtenMountHost(r.Mount.Host) != r.Resolved {
		s += " (written as " + termsafe.Visible(r.Mount.Host) + ")"
	}
	return s
}

// recordEntry is the mount's line in the approval record (mountRecordKey).
func (r WorkspaceMount) recordEntry() string {
	mode := "read-only"
	if r.Mount.RW {
		mode = WritableMountMarker
	}
	return mode + " " + r.Resolved + " -> " + r.Dest()
}

// Drift is "" when the mount's source still resolves to the destination the gate approved, and
// otherwise the refusal: the launch binds nothing for a mount whose destination moved.
func (r WorkspaceMount) Drift() string {
	now := resolveMountHost(r.Mount.Host)
	if now == r.Resolved {
		return ""
	}
	return fmt.Sprintf("The mount %s now resolves to %s, not to %s, which is the destination "+
		"that was approved. Nothing was mounted for it. To use the new destination, launch again: "+
		"the config-change prompt shows it so you can approve or decline it",
		termsafe.Visible(r.Mount.Host), termsafe.Visible(now), termsafe.Visible(r.Resolved))
}

// isWorkspaceRequest reports whether the element n was written by yolo-jail.toml or
// yolo-jail.local.toml itself, and by nothing else: a writable request is honored only from the
// readable project files (RC-D24, "don't need to backport that to old configs"). Not from a TOML
// file a JSONC workspace includes, which would backport it, and not from a fragment a TOML file
// includes, so the file a human reviews is the file that asks.
func isWorkspaceRequest(n *srcNode) bool {
	writers := n.writerList()
	if len(writers) == 0 {
		return false
	}
	for _, w := range writers {
		f := w.file
		if f == nil || f.format != configdoc.TOML || f.via != "" {
			return false
		}
		if f.role != editRoleProject && f.role != editRoleLocal {
			return false
		}
		if base := filepath.Base(f.path); base != WorkspaceTOMLConfigName && base != WorkspaceLocalTOMLConfigName {
			return false
		}
	}
	return true
}

// Mounts are every `mounts` element this read's workspace config declares, read-only and
// writable, each resolved now, in order. A writable element that is not a request
// (isWorkspaceRequest) is left out: validation and CheckWorkspaceMounts refuse it, and nothing
// binds it. It is the set the gate asks about and, once approved, the launch binds.
func (r *WorkspaceRead) Mounts() []WorkspaceMount {
	if r == nil || r.Config == nil {
		return nil
	}
	elements, indices := parseMountsIndexed(r.Config)
	node := r.sources.node().key(mountsKey)
	var out []WorkspaceMount
	for n, m := range elements {
		if m.RW && !isWorkspaceRequest(node.elem(indices[n])) {
			continue
		}
		out = append(out, WorkspaceMount{Mount: m, Resolved: resolveMountHost(m.Host)})
	}
	return out
}

// WritableRequests are the writable ones of Mounts: RC-D24's requests.
func (r *WorkspaceRead) WritableRequests() []WorkspaceMount {
	var out []WorkspaceMount
	for _, m := range r.Mounts() {
		if m.Mount.RW {
			out = append(out, m)
		}
	}
	return out
}

// mountElements are every element of cfg, each resolved through resolved (a source the gate
// already resolved) or now: what the record and the prompt show.
func mountElements(cfg *jsonx.OrderedMap, resolved map[string]string) []WorkspaceMount {
	if cfg == nil {
		return nil
	}
	var out []WorkspaceMount
	for _, m := range ParseMounts(cfg) {
		r, ok := resolved[m.Host]
		if !ok {
			r = resolveMountHost(m.Host)
		}
		out = append(out, WorkspaceMount{Mount: m, Resolved: r})
	}
	return out
}

func resolvedHosts(mounts []WorkspaceMount) map[string]string {
	out := map[string]string{}
	for _, r := range mounts {
		out[r.Mount.Host] = r.Resolved
	}
	return out
}

// mountLines are the lines of the writable (rw) or the read-only mounts among mounts.
func mountLines(mounts []WorkspaceMount, rw bool) []string {
	var out []string
	for _, r := range mounts {
		if r.Mount.RW == rw {
			out = append(out, r.Line())
		}
	}
	return out
}

// withMountRecord is part with the workspace mounts' resolved sources added under
// mountRecordKey; part itself is never changed.
func withMountRecord(part, cfg *jsonx.OrderedMap, resolved map[string]string) *jsonx.OrderedMap {
	mounts := mountElements(cfg, resolved)
	if len(mounts) == 0 {
		return part
	}
	out := jsonx.NewOrderedMap()
	for _, k := range part.Keys() {
		v, _ := part.Get(k)
		out.Set(k, v)
	}
	entries := make([]any, 0, len(mounts))
	for _, r := range mounts {
		entries = append(entries, r.recordEntry())
	}
	out.Set(mountRecordKey, entries)
	return out
}

// CheckWorkspaceMounts is every refusal a workspace mount meets, run on exactly what the gate
// read: no mount in a place the agent can write (mountPathRefusal) and the broker mount fence for
// every one, and for a writable request the user's ceiling and the refusal set (rwMountRefusal)
// on the resolved destination. Each is also a `yolo check` error, but the launch validated an
// earlier read; the gate's read is the one approved and bound. None in a jail for its own
// workspace, whose sources are host paths the host already judged.
func CheckWorkspaceMounts(workspace string, mounts []WorkspaceMount, resolver LoopholeResolver) []string {
	if len(mounts) == 0 || (inJail() && jailOwnWorkspace(workspace)) {
		return nil
	}
	var out []string
	user := validationUserScope()
	allowed := userAllowsWritableMountRequests(user)
	fences := brokerMountFences(resolver)
	for i, r := range mounts {
		if r.Mount.RW && !allowed {
			out = append(out, writableMountRequestRefusal(r.Mount))
			continue
		}
		if why := mountPathRefusal(r.Mount, workspace, otherMounts(mounts, i, user)); why != "" {
			out = append(out, why)
			continue
		}
		if r.Mount.RW {
			if why := rwMountRefusal(r.Resolved, workspace); why != "" {
				out = append(out, fmt.Sprintf("config.mounts: %s is refused: %s", termsafe.Visible(r.Mount.Spec), why))
			}
		}
		if f, ok := fences.reach(r.Resolved); ok {
			out = append(out, workspaceFenceRefusal(r.Mount.Spec, f))
		}
	}
	return out
}

// otherMounts are the mounts beside mounts[skip]: the workspace's others, and every mount the
// user config declares.
func otherMounts(mounts []WorkspaceMount, skip int, user *jsonx.OrderedMap) []ContextMount {
	out := make([]ContextMount, 0, len(mounts))
	for i, r := range mounts {
		if i != skip {
			out = append(out, r.Mount)
		}
	}
	if user != nil {
		out = append(out, ParseMounts(user)...)
	}
	return out
}

// mountPathRefusal is oq-cx-symlink's one link rule for a workspace mount m: "" when its WRITTEN
// source (writtenMountHost: no link followed) is outside the workspace and outside every one of
// others, else the refusal with its next step. Another mount is matched both as written and as
// resolved; a mount spelled exactly as m is m written twice, not a mount in a mount.
func mountPathRefusal(m ContextMount, workspace string, others []ContextMount) string {
	written := writtenMountHost(m.Host)
	for _, ws := range []string{filepath.Clean(workspaceOrCwd(workspace)), resolveExistingPrefix(workspaceOrCwd(workspace))} {
		if pathWithin(written, ws) {
			return fmt.Sprintf("config.mounts: %s is inside the workspace %s, which the jail already "+
				"has at /workspace, and a symbolic link there is the agent's to point anywhere. Write a "+
				"directory outside the workspace as the source, or remove the entry",
				termsafe.Visible(m.Spec), termsafe.Visible(ws))
		}
	}
	for _, o := range others {
		if o.Spec == m.Spec && o.Host == m.Host {
			continue
		}
		outer := writtenMountHost(o.Host)
		for _, p := range []string{outer, resolveExistingPrefix(outer)} {
			if pathWithin(written, p) {
				return fmt.Sprintf("config.mounts: %s is inside the mount %s (%s): a mount may not sit "+
					"inside another, since what is inside one is reachable through it and a symbolic "+
					"link there is not yours to vouch for. Write a directory outside it as the source, "+
					"or remove one of the two",
					termsafe.Visible(m.Spec), termsafe.Visible(p), termsafe.Visible(o.Spec))
			}
		}
	}
	return ""
}

// ApproveWorkspaceRead is the launch's config-change gate over its own read (ApproveConfigAndScope
// with the read's mounts): it resolves each workspace mount ONCE, records those destinations in
// the approval record, shows them, and returns them as what was approved, so the launch binds the
// destinations the human saw. approved is nil unless ok.
func ApproveWorkspaceRead(workspace string, read *WorkspaceRead, scope *ScopeCheck, isTTY, acceptNonInteractive bool,
	prompter ChangePrompter) (ok bool, scopes map[string][]ScopeRepo, approved []WorkspaceMount, err error) {
	mounts := read.Mounts()
	var cfg *jsonx.OrderedMap
	if read != nil {
		cfg = read.Config
	}
	ok, scopes, err = approveConfigAndScope(workspace, cfg, resolvedHosts(mounts), scope, isTTY, acceptNonInteractive, prompter)
	if !ok || err != nil {
		return ok, scopes, nil, err
	}
	return ok, scopes, mounts, nil
}

// RecordApprovalOfRead is RecordApproval over the gate's own read, recording each workspace mount
// at the destination mounts resolved it to: host `yolo check --accept-config-changes`, which
// shows those destinations first.
func RecordApprovalOfRead(workspace string, read *WorkspaceRead, mounts []WorkspaceMount, scope *ScopeCheck) error {
	var cfg *jsonx.OrderedMap
	if read != nil {
		cfg = read.Config
	}
	return recordApproval(workspace, cfg, resolvedHosts(mounts), scope)
}

// ApprovedWorkspaceMounts are the workspace's mounts as the gate would read them now, when the
// approval record already holds them exactly (resolved destinations included), and none
// otherwise. It is what a path that asks nothing (an attach, its briefing, the pending-change
// check) may treat as approved: a mount the record does not hold is not.
func ApprovedWorkspaceMounts(workspace string) []WorkspaceMount {
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		return nil
	}
	mounts := read.Mounts()
	if len(mounts) == 0 {
		return nil
	}
	current, err := SnapshotJSON(approvalConfigPartWith(read.Config, resolvedHosts(mounts)))
	if err != nil {
		return nil
	}
	old, err := os.ReadFile(ApprovalSnapshotPath(workspace))
	if err != nil || respellLegacyRecord(pyRstrip(string(old))) != current {
		return nil
	}
	return mounts
}

// ConfigPartChangedOfRead is ConfigPartChanged for the gate's own read with its mounts resolved
// as mounts: whether recording it would change the record.
func ConfigPartChangedOfRead(workspace string, read *WorkspaceRead, mounts []WorkspaceMount) bool {
	var cfg *jsonx.OrderedMap
	if read != nil {
		cfg = read.Config
	}
	current, err := SnapshotJSON(approvalConfigPartWith(cfg, resolvedHosts(mounts)))
	if err != nil {
		return true
	}
	old, err := os.ReadFile(ApprovalSnapshotPath(workspace))
	return err != nil || respellLegacyRecord(pyRstrip(string(old))) != current
}

// MountSourceBlock is the plain block the gate shows for read-only workspace mounts' lines
// (WorkspaceMount.Line): one per mount, so the destination a link leads to is seen before it is
// approved. nil for none.
func MountSourceBlock(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, "   read-only mount: "+l)
	}
	out = append(out, "   Approving this config approves "+itOrThem(len(lines))+" at the path shown; a "+
		"source that later resolves elsewhere asks again.")
	return out
}

// mountFence is one path the broker mount fence keeps out of a workspace mount.
type mountFence struct{ path, owner, what string }

// mountFences are the broker mount fence's paths for one resolver.
type mountFences []mountFence

// brokerMountFences are the paths fenced by selected brokered, host-daemon and call-broker loopholes.
// A brokered loophole also arms yolo's broker directory and its declared credential paths.
func brokerMountFences(resolver LoopholeResolver) mountFences {
	if resolver == nil || inJail() {
		return nil
	}
	known, _ := resolver.Known()
	var brokerNames, stateNames, credentialNames []string
	for name, info := range known {
		if info.Brokered != nil {
			brokerNames = append(brokerNames, name)
		}
		if (info.HasHostDaemon || info.CallBroker != nil) && info.StateDir != "" {
			stateNames = append(stateNames, name)
		}
		if (info.Brokered != nil && len(info.Brokered.CredentialPaths) > 0) || (info.CallBroker != nil && len(info.CallBroker.CredentialPaths) > 0) {
			credentialNames = append(credentialNames, name)
		}
	}
	if len(brokerNames) == 0 && len(stateNames) == 0 && len(credentialNames) == 0 {
		return nil
	}
	sortStrings(brokerNames)
	sortStrings(stateNames)
	sortStrings(credentialNames)
	var fences mountFences
	if len(brokerNames) > 0 {
		fences = append(fences, mountFence{paths.BrokerDir(), strings.Join(brokerNames, ", "),
			"yolo's broker directory: the brokers' store, each launch's repository scope and the " +
				"audit log of every workspace's brokered calls"})
	}
	for _, name := range stateNames {
		reason := "the state of the " + name + " host daemon, which holds credentials the jail must not read"
		if known[name].CallBroker != nil {
			reason = "the state directory reserved for call broker " + name + ", which is host-owned and not jail input"
		}
		fences = append(fences, mountFence{known[name].StateDir, name, reason})
	}
	for _, name := range credentialNames {
		var declared []string
		if known[name].Brokered != nil {
			declared = append(declared, known[name].Brokered.CredentialPaths...)
		}
		if known[name].CallBroker != nil {
			declared = append(declared, known[name].CallBroker.CredentialPaths...)
		}
		for _, p := range brokerscope.FencedPaths(declared, os.Getenv("HOME"), os.Getenv)[1:] {
			fences = append(fences, mountFence{p, name, "the host credential the " + name +
				" loophole keeps out of the jail"})
		}
	}
	return fences
}

// reach is the fence source reaches, if any.
func (fs mountFences) reach(source string) (mountFence, bool) {
	if len(fs) == 0 {
		return mountFence{}, false
	}
	fencedPaths := make([]string, len(fs))
	for i, f := range fs {
		fencedPaths[i] = f.path
	}
	reached := brokerscope.Reaches(source, fencedPaths)
	if reached == "" {
		return mountFence{}, false
	}
	for _, f := range fs {
		if f.path == reached {
			return f, true
		}
	}
	return mountFence{}, false
}

// workspaceFenceRefusal is the fence's refusal for a workspace entry spec reaching f.
func workspaceFenceRefusal(spec string, f mountFence) string {
	return fmt.Sprintf("config.mounts: %q reaches %s, %s (%s). A workspace config may not "+
		"mount it: the agent can edit that file, and the mount would carry it into the next "+
		"launch. Remove the entry; the same entry in %s is allowed and disclosed",
		spec, f.path, f.what, f.owner, paths.UserConfigDisplayPath())
}
