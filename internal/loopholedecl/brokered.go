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
//  1. reads the workspace's remotes on `remote_host` (every remote URL, verbatim, for `*`) as
//     text at every fresh launch that
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
	// RemoteHost is the forge whose remotes make up the scope: `github.com`. AnyRemoteHost
	// (`*`) instead makes the scope every remote URL of the workspace, on any host, matched
	// exactly (the remote-url scope form, brokerscope.FormRemoteURL; docs/reference/git-broker.md
	// ID-2), and such a source takes no `brokered.<source>.repos` entry.
	RemoteHost string
	// CredentialPaths are host paths holding the source's credential, fenced from
	// `mounts` (BB-D26). An entry is `~/…`, an absolute path, or `$VAR` / `$VAR/…`, which
	// expands from the host environment and is dropped when the variable is unset.
	CredentialPaths []string
	// Grants are the kinds of user grant the source defines (docs/reference/git-broker.md ID-10,
	// BB-D96): `create-branch` for git. Declaring a kind grants nothing: the user's own config
	// lists the kinds a workspace may hold (`brokered.<source>.grant_kinds`), and the workspace
	// config holds each grant, which joins only at the config-change gate. `read` is implied by
	// the loophole being on and needs no declaration.
	Grants []string
	// ReadCapability and KindCapabilities explicitly name canonical capabilities for readable
	// config validation. These metadata fields grant no runtime capability.
	ReadCapability      string
	KindCapabilities    map[string]string
	PendingCapabilities []string
	// UnqualifiedRequests is `origin` when the source's unqualified request lines (`[requests.<source>]`)
	// are for the clone's `origin` remote alone, and "" when they reach every remote on the host
	// (GW-D79). Only a source on one host may declare it; the empty value is today's reach.
	UnqualifiedRequests string
}

// UnqualifiedRequestsOrigin is the `unqualified_requests` value that narrows a source's
// unqualified request lines to the workspace's `origin` remote.
const UnqualifiedRequestsOrigin = "origin"

// AnyRemoteHost is the `remote_host` that declares a source on any host. It is the token alone,
// never a glob: `*.com` is refused. brokerscope.AnyRemoteHost is the same token, for the code
// that reads a scope and does not import this package.
const AnyRemoteHost = "*"

const (
	keyBrokered            = "brokered"
	keySource              = "source"
	keyRemoteHost          = "remote_host"
	keyCredentialPaths     = "credential_paths"
	keyGrants              = "grants"
	keyReadCapability      = "read_capability"
	keyKindCapabilities    = "kind_capabilities"
	keyPendingCapabilities = "pending_capabilities"
	keyUnqualifiedRequests = "unqualified_requests"
)

// brokeredKeys is the block's census. Like a settings declaration it has one voice,
// refusal, in both decoders: an unknown key in a fence is a fence that silently does not
// exist, which is the direction the version-boundary tolerance must not fail in.
var brokeredKeys = []string{keySource, keyRemoteHost, keyCredentialPaths, keyGrants, keyReadCapability, keyKindCapabilities, keyPendingCapabilities, keyUnqualifiedRequests}

var (
	sourceRE     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	remoteHostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	credPathRE   = regexp.MustCompile(`^(~(/.*)?|/.*|\$[A-Z_][A-Z0-9_]*(/.*)?)$`)
	capabilityRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// ValidBrokerSourceName reports whether s is a source token accepted by the brokered manifest.
func ValidBrokerSourceName(s string) bool { return sourceRE.MatchString(s) }

// ValidGrantKind reports whether s spells a grant kind, as the manifest, the user's `grant_kinds`
// and a workspace grant all do: a lowercase letter, then lowercase letters, digits and hyphens.
func ValidGrantKind(s string) bool { return sourceRE.MatchString(s) }

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
	if host != AnyRemoteHost && !remoteHostRE.MatchString(host) {
		return nil, Errorf("%s: 'brokered.remote_host' must be a lowercase hostname such as "+
			"\"github.com\", or \"*\" alone for a source whose scope is every remote URL of the "+
			"workspace, on any host", manifestPath)
	}
	b.RemoteHost = host
	if v := getOrNil(m, keyUnqualifiedRequests); v != nil {
		value, _ := v.(string)
		if value != UnqualifiedRequestsOrigin {
			return nil, Errorf("%s: 'brokered.unqualified_requests' must be %q, the only value it takes", manifestPath, UnqualifiedRequestsOrigin)
		}
		if host == AnyRemoteHost {
			return nil, Errorf("%s: 'brokered.unqualified_requests' is refused on a source with remote_host \"*\": its scope is every remote URL, so there is no one origin to narrow to", manifestPath)
		}
		b.UnqualifiedRequests = value
	}
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
	if v := getOrNil(m, keyGrants); v != nil {
		list, isList := v.([]any)
		if !isList || !AllStrings(list) {
			return nil, Errorf("%s: 'brokered.grants' must be a list of grant kinds such as "+
				"[\"create-branch\"]", manifestPath)
		}
		for _, k := range StringSlice(list) {
			if !ValidGrantKind(k) {
				return nil, Errorf("%s: 'brokered.grants' entry %q is not a grant kind: a lowercase "+
					"letter, then lowercase letters, digits and hyphens, such as \"create-branch\"",
					manifestPath, k)
			}
			if inList(k, b.Grants) {
				return nil, Errorf("%s: 'brokered.grants' names %q twice; list each kind once", manifestPath, k)
			}
			b.Grants = append(b.Grants, k)
		}
	}
	if err := parseBrokeredCapabilityMetadata(manifestPath, m, b); err != nil {
		return nil, err
	}
	return b, nil
}

func parseBrokeredCapabilityMetadata(manifestPath string, m *jsonx.OrderedMap, b *Brokered) error {
	readValue, hasRead := m.Get(keyReadCapability)
	kindsValue, hasKinds := m.Get(keyKindCapabilities)
	pendingValue, hasPending := m.Get(keyPendingCapabilities)
	if !hasRead && !hasKinds && !hasPending {
		return nil
	}
	readCapability, ok := readValue.(string)
	if !hasRead || !ok || !capabilityRE.MatchString(readCapability) || reservedReadableCapability(readCapability) {
		return Errorf("%s: 'brokered.read_capability' must be a non-reserved canonical capability name", manifestPath)
	}
	b.ReadCapability = readCapability
	var pending []string
	if hasPending {
		values, ok := pendingValue.([]any)
		if !ok || !AllStrings(values) {
			return Errorf("%s: 'brokered.pending_capabilities' must be a list of mapped canonical capability names", manifestPath)
		}
		for _, capability := range StringSlice(values) {
			if !capabilityRE.MatchString(capability) || capability == readCapability || inList(capability, pending) {
				return Errorf("%s: 'brokered.pending_capabilities' entry %q must uniquely name a non-read canonical capability", manifestPath, capability)
			}
			pending = append(pending, capability)
		}
	}
	if hasKinds {
		kindMap, ok := kindsValue.(*jsonx.OrderedMap)
		if !ok {
			return Errorf("%s: 'brokered.kind_capabilities' must be a mapping of declared grant kinds to canonical capability names", manifestPath)
		}
		b.KindCapabilities = make(map[string]string, kindMap.Len())
		seen := map[string]string{}
		for _, kind := range kindMap.Keys() {
			capValue, _ := kindMap.Get(kind)
			capability, ok := capValue.(string)
			if !ValidGrantKind(kind) || kind == "read" {
				return Errorf("%s: 'brokered.kind_capabilities' maps undeclared or reserved grant kind %q", manifestPath, kind)
			}
			if !ok || !capabilityRE.MatchString(capability) || reservedReadableCapability(capability) {
				return Errorf("%s: 'brokered.kind_capabilities.%s' must be a non-reserved canonical capability name", manifestPath, kind)
			}
			if capability == readCapability {
				return Errorf("%s: 'brokered.kind_capabilities.%s' collides with read_capability %q", manifestPath, kind, readCapability)
			}
			if previous, exists := seen[capability]; exists {
				return Errorf("%s: 'brokered.kind_capabilities' maps capability %q more than once (%s and %s)", manifestPath, capability, previous, kind)
			}
			if !inList(kind, b.Grants) && !inList(capability, pending) {
				return Errorf("%s: 'brokered.kind_capabilities.%s' names a grant kind not declared in grants or pending_capabilities", manifestPath, kind)
			}
			seen[capability] = kind
			b.KindCapabilities[kind] = capability
		}
	}
	for _, kind := range b.Grants {
		if kind == "read" {
			return Errorf("%s: 'brokered.grants' must not map the implied read grant kind", manifestPath)
		}
		if b.KindCapabilities[kind] == "" {
			return Errorf("%s: 'brokered.kind_capabilities' must map declared grant kind %q", manifestPath, kind)
		}
	}
	for _, capability := range pending {
		if !containsCapabilityValue(b.KindCapabilities, capability) {
			return Errorf("%s: 'brokered.pending_capabilities' entry %q has no grant-kind mapping", manifestPath, capability)
		}
		b.PendingCapabilities = append(b.PendingCapabilities, capability)
	}
	return nil
}

func reservedReadableCapability(capability string) bool {
	return capability == "repos" || capability == "always" || capability == "on_request"
}

func containsCapabilityValue(mapping map[string]string, capability string) bool {
	for _, value := range mapping {
		if value == capability {
			return true
		}
	}
	return false
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
