package loopholedecl

import (
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// brokered.go is the `brokered` manifest block: a loophole whose host daemon runs a host
// credential's commands on the jail's behalf, only against the workspace's own
// repositories on one forge (docs/design/boundary-broker.md §5.6, §7).
//
// # What core does with it, and what core does not know
//
// Core knows repositories, git remotes and a forge's HOSTNAME, and nothing about the tool
// the daemon runs — the declaration is the whole of what core reads, so a second broker
// for another forge is a second manifest, not a second branch in core. For a loophole
// declaring the block, core:
//
//  1. reads the workspace's remotes on `remote_host` as text at every fresh launch that
//     starts the daemon (BB-D19), and the workspace config's `brokered.<source>.repos` entry
//     (docs/design/workspace-widening.md), and puts the union in front of a human as the
//     labeled scope block at the head of the config-change diff (BB-D30, BB-D31), recorded in
//     the approval record's scope part under `source`;
//  2. writes the list that gate approved to that launch's scope file and hands its path to
//     the daemon through TokenRepositoryScope (BB-D32);
//  3. with the loophole's pack selected, refuses a workspace `mounts` entry reaching
//     yolo's broker directory or any `credential_paths` entry, and discloses one at user
//     scope (BB-D26).
//
// The block is a FENCE and a SCOPE, never a grant: nothing in it widens what a jail can
// reach, so a pack-shipped manifest may carry any value in it.

// TokenRepositoryScope is the per-launch SCOPE FILE token, legal in `host_daemon.cmd` of a
// manifest declaring `brokered`. It resolves, in the run pipeline, to the file the fresh
// launch wrote holding the repository scope a human approved for it: a per-launch fact
// like {socket}, which is why it is not resolved at load with {settings}.
const TokenRepositoryScope = "{repository_scope}"

// Brokered is a loophole's `brokered` block.
type Brokered struct {
	// Source is the key the approval record's scope part files this loophole's list
	// under, the workspace config's `brokered.<source>.repos` key that adds repositories
	// to it, and the store and audit log's `service`: `github`.
	Source string
	// RemoteHost is the forge whose remotes make up the scope: `github.com`.
	RemoteHost string
	// CredentialPaths are host paths holding the source's credential, fenced from
	// `mounts` (BB-D26). An entry is `~/…`, an absolute path, or `$VAR` / `$VAR/…`, which
	// expands from the host environment and is dropped when the variable is unset.
	CredentialPaths []string
}

const (
	keyBrokered        = "brokered"
	keySource          = "source"
	keyRemoteHost      = "remote_host"
	keyCredentialPaths = "credential_paths"
)

// brokeredKeys is the block's census. Like a settings declaration it has one voice,
// refusal, in both decoders: an unknown key in a fence is a fence that silently does not
// exist, which is the direction the version-boundary tolerance must not fail in.
var brokeredKeys = []string{keySource, keyRemoteHost, keyCredentialPaths}

var (
	sourceRE     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	remoteHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	credPathRE   = regexp.MustCompile(`^(~(/.*)?|/.*|\$[A-Z_][A-Z0-9_]*(/.*)?)$`)
)

func parseBrokered(manifestPath string, raw any) (*Brokered, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'brokered' must be a mapping", manifestPath)
	}
	for _, k := range m.Keys() {
		if !inList(k, brokeredKeys) {
			return nil, Errorf("%s: unknown key %q in 'brokered' (known: %s) — the block is a "+
				"fence, and a key it does not know would be a fence that does not exist",
				manifestPath, "brokered."+k, strings.Join(sortedCopy(brokeredKeys), ", "))
		}
	}
	b := &Brokered{}
	src, _ := getOrNil(m, keySource).(string)
	if !sourceRE.MatchString(src) {
		return nil, Errorf("%s: 'brokered.source' must be a lowercase name such as \"github\" "+
			"(it keys the approval record's scope part and the audit log's `service`)", manifestPath)
	}
	b.Source = src
	host, _ := getOrNil(m, keyRemoteHost).(string)
	if !remoteHostRE.MatchString(host) {
		return nil, Errorf("%s: 'brokered.remote_host' must be a lowercase hostname such as "+
			"\"github.com\"", manifestPath)
	}
	b.RemoteHost = host
	if v := getOrNil(m, keyCredentialPaths); v != nil {
		list, isList := v.([]any)
		if !isList || !AllStrings(list) {
			return nil, Errorf("%s: 'brokered.credential_paths' must be a list of strings", manifestPath)
		}
		for _, p := range StringSlice(list) {
			if err := refuseControlChars(manifestPath, "'brokered.credential_paths'", p); err != nil {
				return nil, err
			}
			if !credPathRE.MatchString(p) {
				return nil, Errorf("%s: 'brokered.credential_paths' entry %q must be ~/…, an "+
					"absolute path, or $VAR / $VAR/…", manifestPath, p)
			}
			b.CredentialPaths = append(b.CredentialPaths, p)
		}
	}
	return b, nil
}

// refuseRepositoryScopeTokenMismatch holds the block and its token together, in both
// directions, as the `listen` pair is held: a declared scope the daemon is never handed
// would be approved for nothing, and the token with no declaration resolves to no file.
func refuseRepositoryScopeTokenMismatch(manifestPath string, b *Brokered, hd *HostDaemon, doctor, jail []string) error {
	named := false
	if hd != nil {
		for _, s := range hd.Cmd {
			if strings.Contains(s, TokenRepositoryScope) {
				named = true
			}
		}
	}
	for field, args := range map[string][]string{"'doctor_cmd'": doctor, "'jail_daemon.cmd'": jail} {
		for _, s := range args {
			if strings.Contains(s, TokenRepositoryScope) {
				return Errorf("%s: %s names '%s', which resolves only in 'host_daemon.cmd': the "+
					"scope file is written by a fresh launch for the daemon it starts",
					manifestPath, field, TokenRepositoryScope)
			}
		}
	}
	switch {
	case b != nil && hd != nil && hd.Scope == ScopeHost:
		return Errorf("%s: a 'brokered' loophole's daemon is per jail — its scope file is one "+
			"launch's approval — so 'host_daemon.scope' cannot be %q", manifestPath, ScopeHost)
	case named && b == nil:
		return Errorf("%s: 'host_daemon.cmd' names '%s', but the manifest declares no 'brokered' "+
			"block — the token resolves to the scope file that block makes a launch write",
			manifestPath, TokenRepositoryScope)
	case b != nil && !named:
		return Errorf("%s: 'brokered' is declared but 'host_daemon.cmd' never names '%s' — the "+
			"daemon would never be handed the scope a human approved; pass the token where it "+
			"takes its scope file", manifestPath, TokenRepositoryScope)
	}
	return nil
}
