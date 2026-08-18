// Package loopholedecl is the loophole manifest SCHEMA — what a `manifest.jsonc`
// may say, plus the static validation of it, and nothing about what any of it
// MEANS at runtime.
//
// # Why it is a separate package (docs/design/loophole-packaging.md §3.2)
//
// `internal/packload` cannot import `internal/loopholes`. It is a cycle, and it is
// measured rather than assumed:
//
//	$ go list -f '{{join .Imports "\n"}}' ./internal/loopholes | rg yolo-jail
//	…/internal/config …
//	$ go list -deps ./internal/config | rg packload
//	…/internal/packload
//
// So `loopholes` → `config` → `packload`, and a pack's FOOTPRINT — the one screen
// where the whole trust story lands, which must report the daemon argv a pack
// would run on your machine — would have nothing to decode the payload with. A
// footprint that cannot read the manifest degrades the consent string to a bare
// `loophole <name>`: a string that never changes no matter what the daemon
// becomes. Hence a leaf.
//
// It is dependency-free on the rest of the repo apart from the three measured leaf
// decoders (`json5`, `jsonx`, `pytext`), for the same reason `packdecl`
// (kinds.go) and `pluginpack` (pluginpack.go) are: everything that reads a
// manifest — the host CLI's footprint and install approval, `yolo pack lint`, the
// runtime registry, and the in-jail entrypoint — has to be able to import it, so
// it may import none of them. Verify with:
//
//	$ go list -deps ./internal/loopholedecl | rg yolo-jail
//
// which must show this package plus json5/jsonx/pytext and nothing else.
//
// # What does NOT live here
//
// PARSE + STATIC VALIDATION ONLY. No `exec.LookPath`, no `os.Stat` of anything a
// manifest NAMES, no predicate evaluation, no token substitution against a real
// host path. Those are `internal/loopholes`: `requires` evaluation, state dirs,
// `RuntimeArgsFor`, discovery, `SetEnabled`. Concretely, the fields this package
// returns are RAW — `ca_cert` still says `{state}/ca.crt`, a `host_daemon.cmd`
// still says `{loophole_dir}/srv.py`, a bind-mount host still says
// `${XDG_RUNTIME_DIR}/pulse/native` — because resolving any of them requires
// knowing where yolo keeps state, what the module's real path is, and what the
// environment holds, none of which is a fact about the schema.
//
// The one filesystem touch is LoadDir reading THE MANIFEST ITSELF, which is what
// makes the schema decodable from a directory path by a caller that knows nothing
// about loopholes (the same shape as `pluginpack.Load`). It never stats a
// referent.
package loopholedecl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// ManifestName is the file a loophole module declares itself in.
const ManifestName = "manifest.jsonc"

// Error is a manifest that failed to parse or validate.
//
// One error type with a LIST inside, rather than a []string return, because both
// consumers are real: discovery wants one message to warn with (a malformed
// manifest makes the loophole vanish, so the warning carries the reason), and an
// authoring tool wants the problems one per line. Error() joins them; Problems()
// keeps them apart.
type Error struct {
	problems []string
}

// Errorf builds an Error from one formatted problem.
func Errorf(format string, args ...any) *Error {
	return &Error{problems: []string{fmt.Sprintf(format, args...)}}
}

// Error renders every problem, joined by "; ".
func (e *Error) Error() string { return strings.Join(e.problems, "; ") }

// Problems returns one entry per problem found (never empty for a non-nil Error).
func (e *Error) Problems() []string { return append([]string(nil), e.problems...) }

// Intercept is one hostname a loophole terminates TLS for inside the jail.
type Intercept struct {
	// Host is the intercepted hostname, exactly as declared.
	Host string
}

// JailDaemon is a process supervised INSIDE the container.
type JailDaemon struct {
	// Cmd is the argv, RAW: {jail_loophole_dir} is still a token here. It is
	// refused from carrying {loophole_dir} (the host-side spelling) at load.
	Cmd []string
	// Restart is one of ValidRestarts, defaulted to "on-failure".
	Restart string
}

// HostDaemon is a process spawned on the HOST — the sharpest thing a manifest can
// declare, and the one the footprint reports as "runs code on your machine".
type HostDaemon struct {
	// Cmd is the argv, RAW: {loophole_dir} is still a token, and {socket} /
	// {endpoint} belong to the run pipeline, which substitutes them per launch.
	Cmd []string
	// Env is the daemon's environment, in the manifest's key order.
	Env *EnvMap
	// Publishes is what the daemon itself brings up: PublishesEndpoint (the
	// default — it publishes the endpoint file) or PublishesSocket (it binds a
	// plain AF_UNIX socket and yolo fronts it). Always one of the two after a
	// successful decode.
	Publishes string
	// RequestEnd is how a request ends on the daemon's socket: RequestEndFramed
	// (default) or RequestEndEOF (the front half-closes upstream when the
	// client's request direction ends). Always one of the two after a successful
	// decode.
	RequestEnd string
	// Preamble says whether yolo prepends the CONNECTION PREAMBLE — one framed
	// JSON object naming the jail and the service — to every connection it
	// carries to this daemon. DEFAULTS TO TRUE, so a manifest that says nothing
	// gets the framework's own transport, which is what makes the jail identity
	// on the daemon's audit line host-asserted rather than client-claimed.
	//
	// `false` is the DUMB PIPE opt-out: a daemon whose protocol has no room for
	// a frame it never asked for. It is only enforceable under
	// PublishesSocket — under PublishesEndpoint the listener lives inside the
	// daemon's own process, which never reads this manifest, so there is no
	// channel for the flag but argv or env. That is acceptable because
	// packshipped.go forbids a PACK from publishing endpoints, leaving yolo's
	// own daemons as the only endpoint-shaped ones.
	//
	// Spelled positively rather than as `NoPreamble` on purpose: every struct
	// that copies a HostDaemon field-by-field (internal/loopholes' resolve) drops
	// an unlisted bool to its zero value, and for a default-TRUE field that drop
	// is a silent DOWNGRADE. A test pins the round trip; an inverted spelling
	// would have made the drop the safe direction and the bug invisible.
	Preamble bool
}

// HostBindMount is one host path made visible in the container. Readonly
// defaults true.
type HostBindMount struct {
	// Host is the host-side path, RAW: {loophole_dir} and $VAR references are
	// still unresolved (resolving them needs the module's real path and the
	// environment).
	Host string
	// Container is the mount point inside the jail.
	Container string
	// Readonly defaults to true when the key is absent.
	Readonly bool
}

// Requires declares host-side prerequisites. A nil-valued field means absent;
// the *Set booleans distinguish "explicitly set" from "unset". EVALUATING them is
// `internal/loopholes`' job — this package only records what was asked for.
type Requires struct {
	CommandOnPath    string
	CommandOnPathSet bool
	FileExists       string
	FileExistsSet    bool
}

// Manifest is a decoded `manifest.jsonc`, with defaults applied and every value
// still as the author wrote it. The doc comments here ARE the schema reference.
type Manifest struct {
	// Name is the loophole's name; it must equal the module directory's
	// basename.
	Name string
	// Description is the one-line human summary (briefings, `loopholes list`).
	Description string
	// Version is the declared schema version. Recognized so the strict decoder
	// does not report it as a typo — all three bundled manifests declare
	// `"version": 1` — and deliberately NOT enum-checked: a version only a newer
	// build knows is skew, not structure, and refusing it would brick a jail
	// whose baked entrypoint is one `just load` behind. Nothing reads the value.
	Version int
	// VersionSet distinguishes an absent `version` from `"version": 0`.
	VersionSet bool
	// DefaultEnabled is the PACK AUTHOR's opinion about whether this loophole
	// should be on when nobody has said otherwise — `default_enabled`, and ABSENT
	// MEANS FALSE (docs/design/loophole-activation.md R2). It is not the user's
	// switch and must never be read as one: the user's switch is the CONFIG key
	// `loopholes.<name>.enabled`, which internal/loopholes lays over this value at
	// discovery (discover.go's applyWorkspaceOverrides) and which this rename
	// deliberately did not touch.
	//
	// The two used to share the spelling `enabled`, on opposite defaults, with
	// nothing saying which won — the finding that produced the rename. The field
	// name is the enforcement: `Manifest.DefaultEnabled` and `Loophole.Enabled` can
	// no longer be confused at a call site, and resolve() (load.go) is the one place
	// the first becomes the second.
	DefaultEnabled bool
	// Transport is one of ValidTransports; absent means TransportLoopbackTLS.
	Transport string
	// Lifecycle is one of ValidLifecycles; absent means "external".
	Lifecycle string
	// Intercepts are the hostnames whose TLS the loophole terminates. Non-nil
	// after a successful decode (possibly empty).
	Intercepts []Intercept
	// BrokerIP is the address intercepted hosts resolve to; absent means
	// DefaultBrokerIP.
	BrokerIP string
	// CACert is the CA certificate path, RAW: it may still say {state} or be
	// relative to the module dir.
	CACert string
	// CACertSet is false when `ca_cert` was absent or empty.
	CACertSet bool
	// JailEnv is the environment injected into the jail, in key order. Non-nil
	// after a successful decode.
	JailEnv *EnvMap
	// DoctorCmd is the health-check argv, RAW ({loophole_dir} unsubstituted).
	// It runs on the HOST, so the footprint counts it as host execution.
	DoctorCmd []string
	// DoctorCmdSet distinguishes an absent key from an empty list.
	DoctorCmdSet bool
	// HostDaemon is the host-side process, or nil.
	HostDaemon *HostDaemon
	// JailDaemon is the in-container process, or nil.
	JailDaemon *JailDaemon
	// HostBindMounts are the host paths made visible in the jail.
	HostBindMounts []HostBindMount
	// HostDevices are host device nodes passed through (`--device`).
	HostDevices []string
	// StateFiles narrows what crosses from the per-loophole state dir into the
	// jail: paths RELATIVE to that dir, cleaned, guaranteed not to escape it.
	// nil/empty means the historical whole-directory mount, so a manifest
	// without the key does not change meaning.
	StateFiles []string
	// Requires is the activation precondition, unevaluated.
	Requires Requires
	// Platforms is WHERE this loophole can run at all: `<goos>` or
	// `<goos>/<goarch>` entries, exactly as Go spells them, validated statically
	// (platforms.go) and evaluated host-side by SupportsPlatform.
	//
	// Distinct from Requires, which is a runtime probe ("the thing I need is
	// present"). A compiled Linux daemon on macOS is not a missing prerequisite —
	// there is nothing to install — and reporting it as one sends the reader after
	// a fix that cannot exist (loophole-packaging.md §3.1).
	Platforms []string
	// PlatformsSet is false when `platforms` was absent, which means EVERY
	// platform. It has to be a separate bit rather than len()==0: an empty
	// declared list is refused at load precisely because "supports nothing" and
	// "supports everything" must not share a representation.
	PlatformsSet bool
	// Serves is the list of CAPABILITIES this loophole implements — named jobs, not
	// names for the thing doing the job (docs/design/pack-capabilities.md §1). A pack
	// that supersedes every capability a loophole serves retires it; see
	// internal/loopholes' supersede.go for the rule and capabilities.go here for the
	// schema.
	//
	// A statement about ITSELF, so it is a bare string list and needs no
	// justification. The other verb is `supersedes` on a PACK manifest
	// (internal/packdecl), which is a claim about somebody else's component and
	// therefore costs a mandatory `because`.
	//
	// NIL AND EMPTY MEAN THE SAME THING — not participating. There is deliberately no
	// ServesSet bit: silence must never read as a default claim, so a manifest without
	// the key (every third-party one written before it existed) behaves exactly as it
	// did.
	Serves []string
}

// Decode parses and validates manifest bytes STRICTLY: an unknown key is
// reported.
//
// Strict is right for AUTHORING — `yolo pack lint`, `pack init`, a host-side
// validator. A misspelled key is otherwise a declaration that silently does
// nothing, and the author gets no signal at all: today's loader has no
// unknown-key rejection whatsoever, so `"host_deamon"` reads as a loophole with
// no daemon and the symptom surfaces later as a missing endpoint.
//
// Use DecodeTolerant instead when reading a manifest some OTHER build wrote —
// see its doc for why the strictness has to stop at the version boundary.
//
// dir is the module DIRECTORY. It supplies the error prefix
// (<dir>/manifest.jsonc) and the basename `name` must equal; Decode never
// touches the filesystem, so dir is read purely as a string.
//
// On any problem the manifest is nil and err (an *Error) carries the lot: either
// a valid manifest or a refusal, so no caller has to decide whether a
// half-validated one is usable.
//
// Unknown keys are reported ALL AT ONCE, one problem per key, which is the
// authoring win — a reader fixing three typos should not need three
// edit-check cycles. The STRUCTURAL half still stops at the first problem, which
// is inherited from the loader this replaced and is narrower than
// packdecl.Decode's every-problem contract; widening it means restructuring the
// walk, and doing that in the same change as the extraction would have made
// "behaviour is identical" unverifiable.
func Decode(data []byte, dir string) (*Manifest, error) {
	m, unknown, err := decode(data, dir, true)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		return nil, &Error{problems: unknown}
	}
	return m, nil
}

// DecodeTolerant parses and validates manifest bytes, IGNORING keys this build
// does not know and reporting them in skipped.
//
// The strictness in Decode is right for authoring and wrong across a VERSION
// BOUNDARY, and a pack-shipped loophole crosses one: the host CLI and the
// baked-in-the-image `yolo-entrypoint` come from different places (the CLI is
// `go install`ed or freshly built; the entrypoint is frozen at the last
// `just load`), so a newer CLI staging a manifest that uses a newer key is a
// NORMAL state, not a corruption.
//
// This repo learned it the hard way one level up, with packs: adding a `tier`
// field made every jail refuse to start against an older baked image, with no
// route to recovery except rebuilding it, because the failing manifest was one
// yolo SHIPS. A key the reader cannot use is a feature it cannot render, which is
// a degraded loophole; a key it refuses to read is no jail at all. The first is
// recoverable and the second is not, so the version boundary reads tolerantly.
//
// Structural validation still runs over everything KEPT, so a manifest malformed
// in a way BOTH builds understand — a missing `name`, a `transport` outside the
// enum, a `state_files` entry escaping the state dir — still fails loudly here.
func DecodeTolerant(data []byte, dir string) (m *Manifest, skipped []string, err error) {
	return decode(data, dir, false)
}

// LoadDir reads <dir>/manifest.jsonc and decodes it STRICTLY (see Decode).
//
// This is the seam a caller that knows nothing about loopholes uses: a directory
// path in, a schema out, no runtime vocabulary anywhere in the signature.
func LoadDir(dir string) (*Manifest, error) {
	data, err := readManifest(dir)
	if err != nil {
		return nil, err
	}
	return Decode(data, dir)
}

// LoadDirTolerant reads <dir>/manifest.jsonc and decodes it TOLERANTLY (see
// DecodeTolerant).
func LoadDirTolerant(dir string) (m *Manifest, skipped []string, err error) {
	data, err := readManifest(dir)
	if err != nil {
		return nil, nil, err
	}
	return DecodeTolerant(data, dir)
}

// readManifest is the package's ONLY filesystem access, and it reads the
// manifest itself — never a path the manifest names.
func readManifest(dir string) ([]byte, error) {
	manifestPath := ManifestPath(dir)
	if fi, err := os.Stat(manifestPath); err != nil || !fi.Mode().IsRegular() {
		return nil, Errorf("%s not found", manifestPath)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, Errorf("%s: %s", manifestPath, err)
	}
	return raw, nil
}

// ManifestPath returns where a module directory's manifest lives.
func ManifestPath(dir string) string { return filepath.Join(dir, ManifestName) }

// decode is the shared walk. strict routes unknown keys into the returned
// problems; tolerant returns them as skipped notes.
func decode(data []byte, dir string, strict bool) (*Manifest, []string, error) {
	manifestPath := ManifestPath(dir)

	decoded, err := json5.Decode(data)
	if err != nil {
		// The decoder's exception text is not stable, but only the prefix
		// matters here: discovery skips malformed manifests with a warning.
		return nil, nil, Errorf("%s: %s", manifestPath, err)
	}
	data0, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		// A non-object manifest is unreachable for authored manifests.
		// We degrade to a skippable Error rather than crashing.
		return nil, nil, Errorf("%s: manifest must be a JSON object", manifestPath)
	}

	m, err := walk(data0, manifestPath, filepath.Base(dir))
	if err != nil {
		return nil, nil, err
	}
	return m, unknownKeyNotes(data0, manifestPath, strict), nil
}

// walk validates and lowers the decoded object. Every check here is STATIC: type
// checks, enum membership, the name/directory agreement, the control-character
// refusal, the wrong-half token refusals, and the state_files containment rule.
func walk(data *jsonx.OrderedMap, manifestPath, dirName string) (*Manifest, error) {
	nameV, _ := data.Get(keyName)
	name, nameIsStr := nameV.(string)
	if !nameIsStr || name == "" {
		return nil, Errorf("%s: 'name' is required", manifestPath)
	}
	if err := refuseControlChars(manifestPath, "'name'", name); err != nil {
		return nil, err
	}
	if name != dirName {
		return nil, Errorf(
			"%s: name='%s' disagrees with directory '%s' — they must match",
			manifestPath, name, dirName)
	}

	// A RETIRED top-level key is refused BY NAME, here in the structural walk, and
	// both halves of that placement are load-bearing.
	//
	// IN THE WALK, so BOTH decoders refuse it — strict (authoring) and tolerant (the
	// version boundary) alike. Every other cross-version tolerance in this package
	// exists because a key only a NEWER build knows must not make a loophole vanish;
	// a REMOVED key is the opposite case, and tolerating it means silently applying
	// the new default to a manifest that explicitly asked for the old one.
	//
	// EARLY, before every other structural check, so the reader of a manifest written
	// against the old schema is told about the rename rather than about whatever
	// unrelated thing the walk would have tripped over next.
	//
	// The cost, named rather than discovered: an already-shipped third-party manifest
	// carrying `enabled` now FAILS TO LOAD, so its loophole vanishes with a warning
	// (loadFromDir) instead of quietly changing meaning. That is the fail-CLOSED
	// direction in both readings — `enabled: true` wanted on and gets off, `enabled:
	// false` wanted off and gets off — which is what makes a refusal affordable here
	// where R3's `requires` deletion could not have one.
	for _, k := range data.Keys() {
		if msg := retiredTopKeyRefusal(k); msg != "" {
			return nil, Errorf("%s: %s", manifestPath, msg)
		}
	}

	description := ""
	if dv, ok := data.Get(keyDescription); ok {
		s, isStr := dv.(string)
		if !isStr {
			return nil, Errorf("%s: 'description' must be a string", manifestPath)
		}
		description = s
	}

	// `version` is RECOGNIZED and not type-checked. Recognized so the strict
	// decoder does not report the key all three bundled manifests declare as a
	// typo; not type-checked because nothing reads the value, and a manifest must
	// not VANISH (loadFromDir warns and moves on) over a field no consumer has.
	version, versionSet := 0, false
	if vv, ok := data.Get(keyVersion); ok && vv != nil {
		if lit, isInt := jsonx.AsIntLiteral(vv); isInt {
			version, versionSet = atoiOr(lit, 0), true
		}
	}

	// An absent `transport` means loopback-tls: there is one transport, so the
	// default is it. The old default was "tls-intercept", which was never a
	// transport at all — a manifest that said nothing about transports got a value
	// implying it intercepted TLS.
	transport := TransportLoopbackTLS
	if tv, ok := data.Get(keyTransport); ok {
		transport = Str(tv)
	}
	if !inList(transport, validTransports) {
		return nil, Errorf("%s: transport=%s not in %s%s",
			manifestPath, pytext.Repr(transport), sortedListRepr(validTransports),
			retiredTransportHint(transport))
	}

	lifecycle := "external"
	if lv, ok := data.Get(keyLifecycle); ok {
		lifecycle = Str(lv)
	}
	if !inList(lifecycle, validLifecycles) {
		return nil, Errorf("%s: lifecycle=%s not in %s",
			manifestPath, pytext.Repr(lifecycle), sortedListRepr(validLifecycles))
	}

	intercepts, err := parseIntercepts(manifestPath, orEmptyList(data, keyIntercepts))
	if err != nil {
		return nil, err
	}

	caCert, caCertSet := "", false
	if cv, ok := data.Get(keyCACert); ok {
		if s, isStr := cv.(string); isStr && s != "" {
			if err := refuseControlChars(manifestPath, "'ca_cert'", s); err != nil {
				return nil, err
			}
			caCert, caCertSet = s, true
		}
	}

	jailEnv, err := parseEnvMap(manifestPath, orEmptyMap(data, keyJailEnv), "'jail_env' must be a mapping")
	if err != nil {
		return nil, err
	}

	doctorCmd, doctorCmdSet := []string(nil), false
	if dcv, ok := data.Get(keyDoctorCmd); ok && dcv != nil {
		list, listOK := dcv.([]any)
		if !listOK || !AllStrings(list) {
			return nil, Errorf("%s: 'doctor_cmd' must be a list of strings", manifestPath)
		}
		doctorCmd = StringSlice(list)
		doctorCmdSet = true
		if err := refuseControlCharsIn(manifestPath, "'doctor_cmd'", doctorCmd); err != nil {
			return nil, err
		}
		if err := refuseJailTokenInHostField(manifestPath, "'doctor_cmd'", doctorCmd); err != nil {
			return nil, err
		}
	}

	hostDaemon, err := parseHostDaemon(manifestPath, getOrNil(data, keyHostDaemon))
	if err != nil {
		return nil, err
	}
	jailDaemon, err := parseJailDaemon(manifestPath, getOrNil(data, keyJailDaemon))
	if err != nil {
		return nil, err
	}
	hostBindMounts, err := parseHostBindMounts(manifestPath, getOrNil(data, keyHostBindMounts))
	if err != nil {
		return nil, err
	}
	hostDevices, err := parseHostDevices(manifestPath, getOrNil(data, keyHostDevices))
	if err != nil {
		return nil, err
	}
	stateFiles, err := parseStateFiles(manifestPath, getOrNil(data, keyStateFiles))
	if err != nil {
		return nil, err
	}
	requires, err := parseRequires(manifestPath, getOrNil(data, keyRequires))
	if err != nil {
		return nil, err
	}
	platforms, platformsSet, err := platformsFrom(manifestPath, data)
	if err != nil {
		return nil, err
	}
	serves, err := parseServes(manifestPath, getOrNil(data, keyServes))
	if err != nil {
		return nil, err
	}

	// ABSENT MEANS OFF, and the default lives HERE rather than at any reader, which is
	// what makes "a manifest that says nothing activates nothing" literally true
	// instead of true-at-the-places-somebody-remembered.
	//
	// TYPE-CHECKED, NOT COERCED WITH Truthy, and this is the tightening `enabled`
	// could never have. Truthy("false") is TRUE (a non-empty string), so under the old
	// key the one slip a human is actually likely to make — `"enabled": "false"` — read
	// as ON, and the key was too widely shipped to tighten. `default_enabled` is new in
	// this change, so no manifest anywhere can be relying on the loose coercion, and
	// the direction the slip fails in is the one R4 exists to prevent: a quoted "false"
	// would grant host access on a manifest whose author wrote the word for refusing
	// it. `host_daemon.preamble` is the precedent and states the same rule.
	defaultEnabled := false
	if ev, ok := data.Get(keyDefaultEnabled); ok {
		b, isBool := ev.(bool)
		if !isBool {
			return nil, Errorf("%s: 'default_enabled' must be a boolean — write true or false"+
				" (not %s); it is the pack author's default, and an absent key already means false",
				manifestPath, pytext.Repr(Str(ev)))
		}
		defaultEnabled = b
	}

	brokerIP := DefaultBrokerIP
	if bv, ok := data.Get(keyBrokerIP); ok && Truthy(bv) {
		brokerIP = Str(bv)
	}

	return &Manifest{
		Name:           name,
		Description:    description,
		Version:        version,
		VersionSet:     versionSet,
		DefaultEnabled: defaultEnabled,
		Transport:      transport,
		Lifecycle:      lifecycle,
		Intercepts:     intercepts,
		BrokerIP:       brokerIP,
		CACert:         caCert,
		CACertSet:      caCertSet,
		JailEnv:        jailEnv,
		DoctorCmd:      doctorCmd,
		DoctorCmdSet:   doctorCmdSet,
		HostDaemon:     hostDaemon,
		JailDaemon:     jailDaemon,
		HostBindMounts: hostBindMounts,
		HostDevices:    hostDevices,
		StateFiles:     stateFiles,
		Requires:       requires,
		Platforms:      platforms,
		PlatformsSet:   platformsSet,
		Serves:         serves,
	}, nil
}

func parseIntercepts(manifestPath string, raw any) ([]Intercept, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'intercepts' must be a list", manifestPath)
	}
	out := []Intercept{}
	for _, entry := range list {
		m, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return nil, Errorf("%s: each intercept needs a string 'host'", manifestPath)
		}
		hv, _ := m.Get(keyHost)
		host, isStr := hv.(string)
		if !isStr {
			return nil, Errorf("%s: each intercept needs a string 'host'", manifestPath)
		}
		if err := refuseControlChars(manifestPath, "intercepts[].host", host); err != nil {
			return nil, err
		}
		out = append(out, Intercept{Host: host})
	}
	return out, nil
}

func parseRequires(manifestPath string, raw any) (Requires, error) {
	if raw == nil {
		return Requires{}, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return Requires{}, Errorf("%s: 'requires' must be a mapping", manifestPath)
	}
	var req Requires
	if cv, ok := m.Get(keyCommandOnPath); ok && cv != nil {
		s, isStr := cv.(string)
		if !isStr {
			return Requires{}, Errorf("%s: 'requires.command_on_path' must be a string", manifestPath)
		}
		req.CommandOnPath = s
		req.CommandOnPathSet = true
	}
	if fv, ok := m.Get(keyFileExists); ok && fv != nil {
		s, isStr := fv.(string)
		if !isStr {
			return Requires{}, Errorf("%s: 'requires.file_exists' must be a string", manifestPath)
		}
		req.FileExists = s
		req.FileExistsSet = true
	}
	return req, nil
}

func parseHostBindMounts(manifestPath string, raw any) ([]HostBindMount, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'host_bind_mounts' must be a list", manifestPath)
	}
	out := []HostBindMount{}
	for i, entry := range list {
		m, isMap := entry.(*jsonx.OrderedMap)
		if !isMap {
			return nil, Errorf("%s: host_bind_mounts[%d] must be a mapping", manifestPath, i)
		}
		hostV, _ := m.Get(keyHost)
		hostRaw, hostIsStr := hostV.(string)
		if !hostIsStr || hostRaw == "" {
			return nil, Errorf("%s: host_bind_mounts[%d].host must be a non-empty string", manifestPath, i)
		}
		containerV, _ := m.Get(keyContainer)
		container, contIsStr := containerV.(string)
		if !contIsStr || container == "" {
			return nil, Errorf("%s: host_bind_mounts[%d].container must be a non-empty string", manifestPath, i)
		}
		readonly := true
		if rv, ok := m.Get(keyReadonly); ok {
			b, isBool := rv.(bool)
			if !isBool {
				return nil, Errorf("%s: host_bind_mounts[%d].readonly must be a boolean", manifestPath, i)
			}
			readonly = b
		}
		if err := refuseControlChars(manifestPath,
			fmt.Sprintf("host_bind_mounts[%d].host", i), hostRaw); err != nil {
			return nil, err
		}
		if err := refuseControlChars(manifestPath,
			fmt.Sprintf("host_bind_mounts[%d].container", i), container); err != nil {
			return nil, err
		}
		if err := refuseJailTokenInHostField(manifestPath,
			fmt.Sprintf("host_bind_mounts[%d].host", i), []string{hostRaw}); err != nil {
			return nil, err
		}
		out = append(out, HostBindMount{
			Host:      hostRaw,
			Container: container,
			Readonly:  readonly,
		})
	}
	return out, nil
}

func parseHostDevices(manifestPath string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'host_devices' must be a list", manifestPath)
	}
	out := []string{}
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr || s == "" {
			return nil, Errorf("%s: host_devices[%d] must be a non-empty string", manifestPath, i)
		}
		if err := refuseControlChars(manifestPath, fmt.Sprintf("host_devices[%d]", i), s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// parseStateFiles parses the optional `state_files` list: the subset of the
// per-loophole state dir that is allowed to cross into the jail. ABSENT (or an
// empty list) means the whole state dir is mounted — the historical behavior,
// preserved deliberately so an external manifest without the key keeps its
// meaning.
//
// Entries are paths RELATIVE to the state dir. Absolute paths and any ".."
// escape are rejected here, at load time, so the key can only ever narrow the
// existing mount and never reach outside the directory it is narrowing. The
// check is lexical (filepath.Clean), which is why it belongs in this package:
// nothing about it needs the state dir to exist.
func parseStateFiles(manifestPath string, raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, Errorf("%s: 'state_files' must be a list", manifestPath)
	}
	out := []string{}
	for i, entry := range list {
		s, isStr := entry.(string)
		if !isStr || s == "" {
			return nil, Errorf("%s: state_files[%d] must be a non-empty string", manifestPath, i)
		}
		if err := refuseControlChars(manifestPath, fmt.Sprintf("state_files[%d]", i), s); err != nil {
			return nil, err
		}
		if filepath.IsAbs(s) {
			return nil, Errorf("%s: state_files[%d]=%s must be relative to the state dir",
				manifestPath, i, pytext.Repr(s))
		}
		clean := filepath.Clean(s)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, Errorf("%s: state_files[%d]=%s must stay inside the state dir",
				manifestPath, i, pytext.Repr(s))
		}
		out = append(out, clean)
	}
	return out, nil
}

func parseHostDaemon(manifestPath string, raw any) (*HostDaemon, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'host_daemon' must be a mapping", manifestPath)
	}
	cmdV, _ := m.Get(keyCmd)
	cmdList, isList := cmdV.([]any)
	if !isList || len(cmdList) == 0 || !AllStrings(cmdList) {
		return nil, Errorf("%s: 'host_daemon.cmd' must be a non-empty list of strings", manifestPath)
	}
	env, err := parseEnvMap(manifestPath, orEmptyMapValue(getOrNil(m, keyEnv)), "'host_daemon.env' must be a mapping")
	if err != nil {
		return nil, err
	}
	publishes := PublishesEndpoint
	if pv, ok := m.Get(keyPublishes); ok {
		publishes = Str(pv)
	}
	if !inList(publishes, validPublishes) {
		return nil, Errorf("%s: 'host_daemon.publishes' = %s not in %s",
			manifestPath, pytext.Repr(publishes), sortedListRepr(validPublishes))
	}
	// Defaulted HERE, in the decoder, rather than at any of the places that read
	// it: that is what makes "no manifest declares anything to keep working"
	// literally true — every already-shipped manifest decodes with the preamble
	// ON, and the only way to get it off is to say so.
	//
	// TYPE-CHECKED, NOT COERCED WITH Truthy, and the asymmetry with `enabled` is
	// deliberate rather than an oversight. Truthy("false") is TRUE (a non-empty
	// string), so `"preamble": "false"` — the one slip a human writing this key is
	// actually likely to make, and the one internal/config's validateInlineService
	// already guards the config spelling against — would turn the OPT-OUT into the
	// opt-in. There is no diagnostic anywhere downstream for that: `yolo pack
	// lint`'s strict decode reports unknown KEYS, not wrong types, and the
	// consequence lands inside a third-party daemon as a frame it never asked for
	// (for the common one-frame-per-request shape, its first read consumes the
	// preamble AS the request and it then blocks forever on a request already
	// spent — see hostservice.ServeFrontedUnix's "the one mismatch nothing in this
	// tree can detect"). host_bind_mounts.readonly above is the precedent: a
	// default-TRUE bool whose wrong value is silent gets a type check.
	//
	// Tightening is free here and only here: `preamble` is new in this change, so
	// no shipped manifest can be relying on the loose coercion. `enabled` cannot
	// be given the same treatment for exactly that reason.
	preamble := true
	if pv, ok := m.Get(keyPreamble); ok {
		b, isBool := pv.(bool)
		if !isBool {
			return nil, Errorf("%s: 'host_daemon.preamble' must be a boolean — write "+
				"false (not %s) to opt a dumb-pipe daemon out of the connection preamble",
				manifestPath, pytext.Repr(Str(pv)))
		}
		preamble = b
	}
	requestEnd := RequestEndFramed
	if rv, ok := m.Get(keyRequestEnd); ok {
		requestEnd = Str(rv)
	}
	if !inList(requestEnd, validRequestEnds) {
		return nil, Errorf("%s: 'host_daemon.request_end' = %s not in %s",
			manifestPath, pytext.Repr(requestEnd), sortedListRepr(validRequestEnds))
	}
	cmd := StringSlice(cmdList)
	if err := refuseControlCharsIn(manifestPath, "'host_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	if err := refuseJailTokenInHostField(manifestPath, "'host_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	// Under publishes:"socket" the two tokens DIVERGE: {socket} is the upstream
	// AF_UNIX path the daemon binds, {endpoint} is the file yolo's front
	// publishes. An argv naming {endpoint} there would silently publish nothing
	// while yolo publishes over it, so it is an author error refused with the fix.
	if publishes == PublishesSocket {
		for _, s := range cmd {
			if strings.Contains(s, "{endpoint}") {
				return nil, Errorf(
					"%s: 'host_daemon.cmd' names '{endpoint}' but publishes='socket' —"+
						" under that mode the daemon binds an AF_UNIX socket at the path"+
						" yolo substitutes into '{socket}', and yolo publishes the endpoint"+
						" file in front of it; write '{socket}'", manifestPath)
			}
		}
	}
	return &HostDaemon{
		Cmd: cmd, Env: env, Publishes: publishes, RequestEnd: requestEnd, Preamble: preamble,
	}, nil
}

func parseJailDaemon(manifestPath string, raw any) (*JailDaemon, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'jail_daemon' must be a mapping", manifestPath)
	}
	cmdV, _ := m.Get(keyCmd)
	cmdList, isList := cmdV.([]any)
	if !isList || len(cmdList) == 0 || !AllStrings(cmdList) {
		return nil, Errorf("%s: 'jail_daemon.cmd' must be a non-empty list of strings", manifestPath)
	}
	restart := "on-failure"
	if rv, ok := m.Get(keyRestart); ok {
		restart = Str(rv)
	}
	if !inList(restart, validRestarts) {
		return nil, Errorf("%s: 'jail_daemon.restart' not in %s", manifestPath, sortedListRepr(validRestarts))
	}
	cmd := StringSlice(cmdList)
	if err := refuseControlCharsIn(manifestPath, "'jail_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	if err := refuseHostTokenInJailField(manifestPath, "'jail_daemon.cmd'", cmd); err != nil {
		return nil, err
	}
	return &JailDaemon{Cmd: cmd, Restart: restart}, nil
}

// parseEnvMap builds an insertion-ordered EnvMap from a JSON object, coercing
// each key and value to a string. raw must already be resolved to a value that
// is either an *jsonx.OrderedMap or an empty-map sentinel.
func parseEnvMap(manifestPath string, raw any, mappingErr string) (*EnvMap, error) {
	m, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: %s", manifestPath, mappingErr)
	}
	out := NewEnvMap()
	for _, k := range m.Keys() {
		v, _ := m.Get(k)
		out.Set(k, Str(v))
	}
	return out, nil
}
