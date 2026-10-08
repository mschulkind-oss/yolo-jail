package awsauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// settings.go is the CONFIG SURFACE, and the surface is the loophole `settings`
// mechanism — there is no top-level `aws_auth` config key and there must not be one.
//
// # Why not a top-level key
//
// OQ-SSO4 ruled the profile, the role and the session policy USER-CONFIG ONLY: a
// workspace yolo-jail.jsonc is a file the agent inside the jail can rewrite, so an
// agent that could edit it could otherwise point this service at the `admin` profile.
// `internal/loopholedecl/settings.go` already has exactly that vocabulary — a per-key
// `scope` defaulting to `user`, type-checked, validated host-side, written to a flat
// 0600 JSON file the daemon's argv names through the `{settings}` token. A top-level
// key would be a second scope grammar for one feature, which is the thing that ruling
// refuses.
//
// So THIS file reads the file yolo wrote. It never reads a yolo-jail.jsonc, never
// reads an environment variable, and never takes any of the three from a request: the
// jail does not get to name the profile, role or policy it wants.
//
// # No credential mode is inferred from absence
//
// OQ-SSO1: the role/session-policy arms are optional additional restrictions, and the
// explicitly selected profile-permissions-as-configured arm is available only when asked
// for BY NAME. That choice remains user-scope-only and never follows from omission.

// Setting key names, as declared in the loophole manifest under
// `loopholes.aws-auth.settings`. Spelled as constants because the refusals below
// have to name them and a literal in two places is a literal that drifts.
const (
	// SettingProfile names the AWS profile this service resolves. No default:
	// absent means the service has nothing to serve and refuses at spawn.
	SettingProfile = "profile"
	// SettingRoleARN is the role the N2/N3 arms assume.
	SettingRoleARN = "role_arn"
	// SettingSessionPolicy is the inline session policy JSON the N2 arm attaches.
	SettingSessionPolicy = "session_policy"
	// SettingUnnarrowed is the explicit user-only choice to use the assigned profile's
	// permission set as configured, without an extra AssumeRole or session policy.
	// Bool, default false, so absence never selects this route.
	SettingUnnarrowed = "unnarrowed"
)

// SettingsScope is the config path the keys above live under, as a human edits it.
const SettingsScope = "loopholes.aws-auth.settings"

// settingsScope is how a refusal spells a key for the human: the full config path
// they would edit.
func settingsScope(key string) string { return SettingsScope + "." + key }

// Settings is the flat file `{settings}` names, decoded. Every declared key is
// present in that file (loopholedecl guarantees totality), so absence here means
// only "an older or hand-written file", and each field's zero is the refusing one.
type Settings struct {
	Profile       string `json:"profile"`
	RoleARN       string `json:"role_arn"`
	SessionPolicy string `json:"session_policy"`
	Unnarrowed    bool   `json:"unnarrowed"`
}

// LoadSettings reads the resolved settings file.
//
// A MISSING path and a MISSING file are different from an UNPARSEABLE one, and the
// caller needs the difference: no path means the daemon was run by hand, no file means
// no jail has launched this loophole yet (the normal state of a fresh machine), and a
// file that does not parse is a real fault. Only the last is an error here.
func LoadSettings(path string) (Settings, error) {
	if path == "" {
		return Settings{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Settings{}, nil
		}
		return Settings{}, fmt.Errorf("read aws-auth settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return Settings{}, fmt.Errorf("decode aws-auth settings at %s: %w", path, err)
	}
	return s, nil
}

// NarrowingKind is which of the design's narrowing arms is in force.
type NarrowingKind string

const (
	// NarrowSessionPolicy is N2: AssumeRole plus an inline session policy. The
	// cheapest arm that narrows INSIDE Bedrock.
	NarrowSessionPolicy NarrowingKind = "session-policy"
	// NarrowRole is N3: a purpose-built role, assumed, with no extra policy.
	NarrowRole NarrowingKind = "role"
	// NarrowNone is un-narrowed, asked for by name: serve whatever the permission
	// set grants. Covers both "my permission set is already narrow enough" (N4, and
	// the row people miss — pointing the profile at a narrower permission set you
	// are ALREADY assigned) and "I have nothing to narrow with yet". Never the
	// result of an absent key.
	NarrowNone NarrowingKind = "none"
)

// Narrowing is the resolved narrowing decision. Zero value is not servable — use
// Resolve.
type Narrowing struct {
	Kind          NarrowingKind
	RoleARN       string
	SessionPolicy string
}

// Digest fingerprints the narrowing, so a cache entry minted under a different
// configuration reads as a miss. It covers the policy TEXT, because editing the
// policy without editing the role must invalidate the cache.
func (n Narrowing) Digest() string {
	return Fingerprint(string(n.Kind) + "\x00" + n.RoleARN + "\x00" + n.SessionPolicy)
}

// Describe is the one-line summary of what is in force, for a startup line and for
// the self-check. It names no secret and no policy body.
func (n Narrowing) Describe() string {
	switch n.Kind {
	case NarrowSessionPolicy:
		return "AssumeRole " + n.RoleARN + " with an inline session policy (" +
			Fingerprint(n.SessionPolicy) + ")"
	case NarrowRole:
		return "AssumeRole " + n.RoleARN + " with no additional session policy"
	case NarrowNone:
		return "none — the permission set is served as-is"
	default:
		return "unresolved"
	}
}

// Config is a servable configuration: a profile and a narrowing. Producing one is
// the only way to reach a mint.
type Config struct {
	Profile   string
	Narrowing Narrowing
}

type ResolveRefusalKind string

const (
	RefusalMissingProfile       ResolveRefusalKind = "missing-profile"
	RefusalPolicyWithoutRole    ResolveRefusalKind = "policy-without-role"
	RefusalConflictingNarrowing ResolveRefusalKind = "conflicting-narrowing"
	RefusalInvalidPolicy        ResolveRefusalKind = "invalid-policy"
	RefusalMissingNarrowing     ResolveRefusalKind = "missing-narrowing"
)

// ResolveRefusal keeps the machine-readable cause separate from its legacy detailed error text.
// Pack diagnostics must project Kind to fixed safe text rather than forwarding Error().
type ResolveRefusal struct {
	Kind    ResolveRefusalKind
	Message string
}

func (r *ResolveRefusal) Error() string { return r.Message }

// Resolve turns settings into a servable Config, or REFUSES and says which key to
// write. Every refusal names a full config path, because the person reading it is
// about to edit a file.
//
// # This is the step the design calls expensive if late
//
// The permission mode is explicit now rather than a changed default later: the user chooses
// whether to add role/session-policy restrictions or use the profile permissions as configured.
// Read the refusals in that order — the absent case is the one that matters.
func (s Settings) Resolve() (Config, error) {
	profile := strings.TrimSpace(s.Profile)
	if profile == "" {
		return Config{}, &ResolveRefusal{Kind: RefusalMissingProfile, Message: fmt.Sprintf("no AWS profile is configured: set %s in your USER config "+
			"(~/.config/yolo-jail/config.jsonc) to the profile this service should resolve. It is "+
			"user-scope on purpose — a workspace yolo-jail.jsonc is a file the jail's own agent can "+
			"rewrite", settingsScope(SettingProfile))}
	}
	role := strings.TrimSpace(s.RoleARN)
	policy := strings.TrimSpace(s.SessionPolicy)

	// A policy without a role would be silently unused. Refuse it rather than
	// suggesting that it narrowed the credential when no AssumeRole call can attach it.
	if policy != "" && role == "" {
		return Config{}, &ResolveRefusal{Kind: RefusalPolicyWithoutRole, Message: fmt.Sprintf("%s is set but %s is not: an inline session policy is an "+
			"argument to AssumeRole, so without a role there is nothing to attach it to",
			settingsScope(SettingSessionPolicy), settingsScope(SettingRoleARN))}
	}

	// BOTH ARMS AT ONCE cannot be resolved in the direction that grants. Preferring
	// the role would silently give someone who asked for un-narrowed something
	// narrower (confusing); preferring un-narrowed would silently discard a narrowing
	// they configured (unsafe). Neither is a guess worth making.
	if s.Unnarrowed && role != "" {
		return Config{}, &ResolveRefusal{Kind: RefusalConflictingNarrowing, Message: fmt.Sprintf("%s is true AND %s is set — drop one: the role is a narrowing "+
			"and %s asks for none, so which wins is not something this service should guess",
			settingsScope(SettingUnnarrowed), settingsScope(SettingRoleARN),
			settingsScope(SettingUnnarrowed))}
	}

	switch {
	case role != "" && policy != "":
		if err := validPolicyJSON(policy); err != nil {
			return Config{}, &ResolveRefusal{Kind: RefusalInvalidPolicy, Message: fmt.Sprintf("%s is not a JSON policy document: %v — STS would refuse "+
				"every mint, so this is refused at spawn instead",
				settingsScope(SettingSessionPolicy), err)}
		}
		return Config{Profile: profile, Narrowing: Narrowing{
			Kind: NarrowSessionPolicy, RoleARN: role, SessionPolicy: policy,
		}}, nil
	case role != "":
		return Config{Profile: profile, Narrowing: Narrowing{Kind: NarrowRole, RoleARN: role}}, nil
	case s.Unnarrowed:
		return Config{Profile: profile, Narrowing: Narrowing{Kind: NarrowNone}}, nil
	default:
		// OQ-SSO1: absence never selects the profile-permissions-as-configured route.
		return Config{}, &ResolveRefusal{Kind: RefusalMissingNarrowing, Message: fmt.Sprintf("no AWS permission mode is configured for profile %q: set %s to a "+
			"role this service should assume (optionally add %s to restrict it), or set %s to true "+
			"to use the assigned permission set as configured. This choice is explicit and is never "+
			"the default",
			profile, settingsScope(SettingRoleARN), settingsScope(SettingSessionPolicy),
			settingsScope(SettingUnnarrowed))}
	}
}

// validPolicyJSON checks the session policy parses as a JSON object. It does NOT
// validate the policy language: STS owns that vocabulary and forwarding STS's own
// refusal is better than a second, staler grammar here. What it catches is the whole
// class of shell-quoting and heredoc accidents, at spawn rather than per request.
func validPolicyJSON(policy string) error {
	var doc map[string]any
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		return err
	}
	if len(doc) == 0 {
		return errors.New("it is an empty object")
	}
	if _, ok := doc["Statement"]; !ok {
		keys := make([]string, 0, len(doc))
		for k := range doc {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("it has no \"Statement\" key (found: %s)", strings.Join(keys, ", "))
	}
	return nil
}
