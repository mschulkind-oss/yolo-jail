// Host-scoped daemon DISCOVERY: the set the management verb acts over.
//
// THE SET IS DERIVED, NEVER LISTED. A `host_daemon.scope: "host"` declaration in
// a loophole manifest is what creates a host-wide daemon (loopholedecl.ScopeHost),
// so the manifests are the census and this file reads them — through the same
// converged Set every other host-side surface reads
// (loopholes.NewHostSet, docs/reference/loophole-system.md#selection-and-discovery).
// Nothing here may grow a list of names: the set was one member when `yolo broker`
// was written, two on 2026-09-15 and three on 2026-09-18, and a hardcoded list is
// exactly how the CLI stayed at one while the lifecycle engine generalized.
//
// THE RENDEZVOUS IS THE SECOND SOURCE, and it is not redundant with the first. A
// singleton outlives the config that started it — it survives the jail ending, the
// pack being deselected and the loophole being disabled (host-daemon-ownership.md
// §6, mode 5) — so a set derived from THIS machine's current declarations alone
// would refuse to stop the very daemon a user is trying to get rid of. The PID
// files at paths.HostSingletonPIDFile are a second derivation of the same shape:
// still no list, just the other question ("what is running?") instead of ("what is
// declared?").
//
// What each source can answer differs, and the difference is reported rather than
// hidden: a declared member carries the spawn argv, so `restart` can respawn it; a
// member known only from its rendezvous does not, so `restart` refuses BY NAME and
// points at `stop`, which needs nothing but the PID file.
package broker

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Singleton is one member of the host-scoped set: the loophole NAME every path is
// derived from, plus what this machine can say about it.
//
// The name is the whole identity — paths.HostSingleton* and SingletonLogPath are
// functions of it and of nothing else — so a Singleton with only a Name is already
// enough for status, stop and logs. Argv is the one thing that has to come from a
// declaration, which is why Declared and NoSpawn exist as separate facts: "nothing
// declares it" and "something declares it but yolo may not run it" send a reader to
// different places.
type Singleton struct {
	// Name is the loophole name, e.g. "claude-oauth-broker".
	Name string
	// Description is the manifest's, empty for a rendezvous-only member.
	Description string
	// Declared reports whether a manifest this machine can read declares
	// `host_daemon.scope: "host"` under this name.
	Declared bool
	// Argv is the resolved spawn argv ({socket} substituted, the bare `yolo`
	// token self-exec'd). Empty when yolo cannot or may not spawn it.
	Argv []string
	// NoSpawn is why Argv is empty, phrased for the user, or "" when Argv is set.
	// It is never empty when a caller needs to refuse: Restart prints it.
	NoSpawn string
}

// BrokerSingleton is the Claude OAuth broker's record, built from this package's
// own constants and NOT from discovery.
//
// That independence is the point of it. `yolo broker` is retained as an alias for
// the general verb, and an alias that resolved through pack discovery would stop
// working exactly where discovery is empty — in a jail, on a host whose `packs`
// list does not name claude, inside a command that never staged anything — while
// the daemon it manages is running at a path that has not moved. The alias means
// one name, so it names it.
func BrokerSingleton() Singleton {
	return Singleton{
		Name:        BrokerLoopholeName,
		Description: "Serializes Claude OAuth refreshes so concurrent jails cannot burn the single-use refresh token.",
		Declared:    true,
		Argv:        BrokerSpawnArgv(execx.SelfExecArgv([]string{"yolo"}), BrokerSingletonSocket),
	}
}

// Singletons returns the host-scoped set this machine can manage: every loophole
// declaring `host_daemon.scope: "host"`, joined with every host-wide rendezvous
// that has state on disk, sorted by name.
//
// workspace is the tree a launch would mount :rw, used for the PLACEMENT
// rule — a daemon program an agent can rewrite between launches must not be
// spawned by a management verb either, and the refusal travels on the record as
// NoSpawn rather than being discovered at the moment of the spawn.
//
// EMPTY IS AN HONEST ANSWER, not an error: in a jail, and on a host that has
// resolved no packs, discovery records nothing (discover.go's fail-safe contract),
// and a machine that has never launched has no rendezvous either. The caller says
// so; it does not invent a member.
func Singletons(workspace string) []Singleton {
	return mergeSingletons(DeclaredSingletons(workspace), RendezvousSingletonNames())
}

// DeclaredSingletons is the DECLARATION half of Singletons: the loopholes whose
// manifests declare `host_daemon.scope: "host"`.
func DeclaredSingletons(workspace string) []Singleton {
	set := loopholes.NewHostSet(nil)
	var out []Singleton
	for _, lp := range set.All() {
		hd := lp.HostDaemon
		// ScopeHost, never "not ScopeJail" — the field's zero value is "", and every
		// reader in the tree compares against ScopeHost for the same reason: a record
		// built by hand then reads as per-jail, which is the direction where a dropped
		// field costs a spawn instead of silently claiming a host-wide daemon.
		if hd == nil || hd.Scope != loopholes.ScopeHost {
			continue
		}
		s := Singleton{Name: lp.Name, Description: lp.Description, Declared: true}
		switch {
		case !set.MayRunHostCode(lp):
			// The ORIGIN GATE, evaluated here because this verb spawns host code. A
			// management verb is not a second door into running a pack module nothing in
			// this process resolved. It is not an approval: OQ-TP9
			// (docs/design/trust-paths.md) deleted the fetched-pack approval, and every
			// module pack resolution records passes this gate, so reaching it means the Set
			// was built without resolving packs — the same fail-safe branch, and the same
			// words, as the doctor loop's in internal/loopholes/runtime.go.
			s.NoSpawn = "its pack was not resolved by this process, so nothing vouches " +
				"for the module (a yolo bug, not a config problem — please report it)"
		case len(hd.Cmd) == 0:
			s.NoSpawn = "its manifest declares no command"
		default:
			if probs := lp.PlacementProblems(workspace); len(probs) > 0 {
				s.NoSpawn = probs[0]
			} else {
				s.Argv = SingletonArgv(lp.Name, hd.Cmd)
			}
		}
		out = append(out, s)
	}
	return out
}

// SingletonArgv resolves a declared `host_daemon.cmd` into the argv that will
// actually run: {endpoint}/{socket} substituted with the daemon's own rendezvous
// socket, a leading `~` expanded, and the bare `yolo` launcher token replaced by
// the running binary.
//
// {loophole_dir}, {settings} and {state} are NOT handled here and must not be:
// they are functions of the loophole NAME and are already resolved at record-load
// time (internal/loopholes/load.go), so a second substitution site for them is a
// second answer waiting to disagree. {socket} is the one token the loader cannot
// resolve, because only the framework decides what the host-wide path is.
func SingletonArgv(name string, cmd []string) []string {
	if len(cmd) == 0 {
		return nil
	}
	sock := paths.HostSingletonSocket(name)
	argv := make([]string, 0, len(cmd))
	for _, a := range cmd {
		a = expandUser(a)
		a = strings.ReplaceAll(a, "{endpoint}", sock)
		argv = append(argv, strings.ReplaceAll(a, "{socket}", sock))
	}
	return execx.SelfExecArgv(argv)
}

// rendezvousGlob matches every host-wide daemon's SPAWN LOCK.
//
// THE LOCK, NOT THE PID FILE, and the difference is a process this verb must never
// offer to kill. `/tmp/yolo-<name>.{sock,pid,lock}` are the three rendezvous paths
// (paths.HostSingleton*), but the `.pid` half of that namespace has OTHER OWNERS:
// the in-jail supervisor writes `/tmp/yolo-jaild.pid` and keeps
// `/tmp/yolo-jail-supervisor.pid` as its legacy name
// ([`internal/entrypoint/runtime.go`](../entrypoint/runtime.go)). Measured in this
// repo's own jail 2026-09-20: globbing `.pid` produced a member called `jaild`, and
// `yolo host-daemon stop jaild` would have killed the jail's own supervisor. A
// derivation that round-trips is not enough when two things derive the same way.
//
// `paths.HostSingletonLock` has exactly one writer. The lock is created by the
// ensure before anything else and is never removed — not by BrokerKill, which
// clears the pid file, the socket and the stamp — so it answers the durable
// question ("has a singleton by this name been ensured on this host?") rather than
// the momentary one, which is what a management surface wants: a daemon that was
// stopped is still one a user may ask the status of.
const rendezvousGlob = "/tmp/yolo-*.lock"

// RendezvousSingletonNames is the ON-DISK half of Singletons: the loophole names
// this host has ensured a singleton for, whatever it currently declares.
//
// Silent on every failure, empty on any doubt: a management verb that invented a
// member from an unreadable /tmp would offer to stop a daemon that does not exist.
func RendezvousSingletonNames() []string {
	matches, err := filepath.Glob(rendezvousGlob)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range matches {
		base := filepath.Base(m)
		name := strings.TrimSuffix(strings.TrimPrefix(base, "yolo-"), ".lock")
		if name == "" {
			continue
		}
		// The path must round-trip: a file that merely LOOKS like the pattern is not
		// a rendezvous, and deriving the name back through the same function that
		// mints it is the only check that cannot drift from it.
		if paths.HostSingletonLock(name) != m {
			continue
		}
		out = append(out, name)
	}
	return out
}

// mergeSingletons joins the two derivations, declaration winning on a name both
// produced (it carries the argv), and sorts by name so every listing is stable.
func mergeSingletons(declared []Singleton, running []string) []Singleton {
	byName := map[string]Singleton{}
	for _, s := range declared {
		byName[s.Name] = s
	}
	for _, name := range running {
		if _, ok := byName[name]; ok {
			continue
		}
		byName[name] = Singleton{
			Name: name,
			NoSpawn: "nothing on this machine declares a host-wide daemon by that name, " +
				"so yolo does not know how to start one",
		}
	}
	out := make([]Singleton, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// expandUser expands a leading "~"/"~/…" against $HOME. A "~user" form is left
// untouched — the same narrow rule the run pipeline's spawn path applies, so the
// two resolutions of one manifest field cannot disagree.
func expandUser(p string) string {
	if p == "" || p[0] != '~' {
		return p
	}
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i != 1 {
		return p // ~user form
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, p[1:])
}
