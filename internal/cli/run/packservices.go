package run

// packservices.go composes the launch's SERVICE contributions
// (packdecl.KindService, docs/design/wire-bridge.md §2.1) into the container
// argv. In this build exactly one thing is composed: a service's jail_daemon
// joins the YOLO_JAIL_DAEMONS payload through internal/loopholes'
// RuntimeArgsForWithJailDaemons — the loophole JailDaemon wire shape verbatim
// ({name, cmd, restart}), because the env var is ONE frozen contract with ONE
// writer (the in-jail reader is the supervisor's ParseEnv, and the source-skew
// gate cannot see an env contract).
//
// host_daemon is DELIBERATELY NOT composed anywhere in this build: no
// host-daemon path exists for services yet (wire-bridge.md §2.1 rules one kind
// carries both halves; executing the host half is the follow-up that re-forms
// the loophole packs around services). The manifest validates the declaration
// so it is stateable today; this file is where its composition will land, and
// this comment is the standing record that ignoring it here is a decision, not
// an oversight. The same is true of a service's `platforms`, `serves` and
// `settings` — declared, carried, unread — with no consumer in the tree yet.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// serviceJailDaemons returns the YOLO_JAIL_DAEMONS payload entries for every
// selected pack's service contribution that declares a jail_daemon, sorted by
// service name. Sorted, not declaration-ordered, because the entries ride one
// JSON list beside the loopholes' own and the env var is read in-jail verbatim:
// a deterministic argv is the same rule the pack env block above it follows.
//
// The restart policy is emitted ALWAYS, defaulting to "on-failure" when the
// manifest said nothing — the same default the supervisor's ParseEnv applies
// when the key is absent, spelled out so the payload's shape is exactly the
// loophole half's (internal/loopholes' runtimeArgsFor sets the key the same
// way) and a diff of the two halves' entries shows no structural difference.
func serviceJailDaemons(packs []*packload.Pack) []any {
	type named struct {
		name string
		spec *jsonx.OrderedMap
	}
	var entries []named
	for _, p := range packs {
		if p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			if s.JailDaemon == nil || len(s.JailDaemon.Cmd) == 0 {
				continue
			}
			spec := jsonx.NewOrderedMap()
			spec.Set("name", s.Name)
			cmd := make([]any, len(s.JailDaemon.Cmd))
			for i, c := range s.JailDaemon.Cmd {
				cmd[i] = c
			}
			spec.Set("cmd", cmd)
			restart := s.JailDaemon.Restart
			if restart == "" {
				restart = "on-failure"
			}
			spec.Set("restart", restart)
			entries = append(entries, named{name: s.Name, spec: spec})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	if len(entries) == 0 {
		return nil
	}
	out := make([]any, len(entries))
	for i, e := range entries {
		out[i] = e.spec
	}
	return out
}
