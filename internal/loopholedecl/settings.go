package loopholedecl

import (
	"regexp"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// settings.go is the manifest's SETTINGS DECLARATION — the half of
// docs/design/pack-config-keys.md that lets a loophole own a config key instead
// of core naming it by hand.
//
// # Why the keys are declared and typed rather than an opaque map
//
// The obvious design — `loopholes.<name>.settings` as a free-form object core
// passes through — is the one the design doc kills in its first callout, and the
// reason is not tidiness. If core validates only "it is an object" it cannot tell
// `settings.visible` from `settings.ld_preload`, and the user-scope-only refusal on
// `env` exists precisely to keep LD_PRELOAD out of a host daemon's spawn
// environment (internal/config/validate_loopholes.go). An opaque map does not dodge
// that rule; it launders it. So the schema is closed at both ends: a fixed type set
// here, and a per-key `scope` that says who may supply a value.
//
// # The type set is CLOSED and deliberately small
//
// `string`, `bool`, `int`, `string_list`. Widening a closed set later is additive
// and safe; narrowing one is not, which is why it starts narrow. A free-form JSON
// Schema was considered and refused: an arbitrary schema language is a second
// config system, and validation would leave core's hands.

// Setting value types. The whole set — a `type` outside it is refused, in BOTH
// decoders (see parseSettings).
const (
	SettingTypeString     = "string"
	SettingTypeBool       = "bool"
	SettingTypeInt        = "int"
	SettingTypeStringList = "string_list"
)

// validSettingTypes is the closed set, for membership and for the error message.
var validSettingTypes = []string{
	SettingTypeString, SettingTypeBool, SettingTypeInt, SettingTypeStringList,
}

// Setting scopes — WHICH config file may supply a value for this key.
//
// SettingScopeUser means the user config only (~/.config/yolo-jail/config.jsonc):
// a workspace file contributing to the key is refused, exactly as `env` is. That is
// the declaration a capability-WIDENING key wants, and docs/design/pack-config-keys.md
// §3 is why it has to be sayable at all: MergeConfig union-merges every list at every
// depth, so a user-scope ceiling that a workspace NARROWS is inexpressible — a
// workspace can only ever add. For an allowlist that inverts the intended safety
// property, and `scope: "user"` is the only way to state it.
//
// SettingScopeWorkspace means EITHER scope may supply it — the weaker declaration,
// named for the weaker file. It is safe because a workspace-supplied value reaches a
// host daemon only through the config-change approval gate, which became a real
// control when the approval snapshot moved host-side and non-interactive launches
// stopped auto-accepting (OQ-K2, and see the dated warning in that section: this
// answer travels with those two).
const (
	SettingScopeUser      = "user"
	SettingScopeWorkspace = "workspace"
)

var validSettingScopes = []string{SettingScopeUser, SettingScopeWorkspace}

// DefaultSettingScope is what an ABSENT `scope` means: user-scope only.
//
// SILENCE IS THE SAFE CHOICE, and it is the safe choice in the direction that
// matters. A settings value can reach a host daemon's argv-named file, so an author
// who says nothing about who may supply it gets the answer `env` already gets —
// user-only. Widening to `workspace` is then a deliberate, greppable act in the
// manifest, which is what makes it auditable at `yolo pack footprint` time rather
// than discoverable by reading core.
const DefaultSettingScope = SettingScopeUser

// settingKeyRe bounds a setting's KEY NAME. Lowercase snake_case, matching how every
// config key in this project is already spelled (`visible`, `fields`,
// `default_enabled`) — a closed shape rather than a general identifier, because these
// names become keys in a JSON file core writes and a host daemon reads, and a name
// that round-trips through neither is a name nobody should be able to declare.
var settingKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Setting is ONE declared settings key: what it is called, what type of value it
// takes, who may supply one, and what it holds when nobody does.
//
// The doc comments here ARE the reference for the `settings` block, the same way
// Manifest's are for the manifest.
type Setting struct {
	// Key is the name a config supplies under `loopholes.<name>.settings`.
	Key string
	// Type is one of the four constants above. Always set after a successful
	// decode — it is a required key, because a declaration with no type is a
	// declaration core cannot validate against, which OQ-K1 makes a refusal.
	Type string
	// Scope is SettingScopeUser or SettingScopeWorkspace, defaulted to
	// DefaultSettingScope. Always one of the two after a successful decode.
	Scope string
	// Description is the one-line human summary. Optional.
	Description string
	// Default is the value the resolved settings file carries when no config
	// supplies one, already coerced to the Go shape the declared type implies:
	// string, bool, int, []string. NEVER nil after a successful decode — an
	// absent `default` becomes the TYPE'S ZERO (empty string, false, 0, empty
	// list) rather than a null.
	//
	// That totality is the point: the file core writes carries every declared key,
	// so a daemon reading it never has to distinguish "absent" from "false", and
	// the flat-map contract in pack-config-keys.md §6 stays a flat map. For
	// `host_processes.visible` the type zero is the empty list, which is exactly
	// what an unset allowlist meant before this existed.
	Default any
	// DefaultSet distinguishes a declared `default` from the type zero standing in
	// for an absent one. Nothing in core branches on it today; it exists so an
	// authoring tool can render the declaration back without inventing a key.
	DefaultSet bool
}

// SettingByKey finds a declaration by key. Linear over a list a manifest author
// wrote by hand — there is no map because ORDER is load-bearing: the settings file
// is written in declaration order so its bytes are stable across launches.
func SettingByKey(settings []Setting, key string) (Setting, bool) {
	for _, s := range settings {
		if s.Key == key {
			return s, true
		}
	}
	return Setting{}, false
}

// SettingKeys returns the declared keys in DECLARATION order (not sorted) — the
// order the resolved settings file uses.
func SettingKeys(settings []Setting) []string {
	out := make([]string, len(settings))
	for i, s := range settings {
		out[i] = s.Key
	}
	return out
}

// parseSettings decodes the `settings` block.
//
// # Every problem here is a REFUSAL, in the tolerant decoder too
//
// This is the one place in this package where the version-boundary tolerance
// DecodeTolerant exists for is deliberately not applied, and OQ-K1's closing note
// is the ruling: *"core must never hand a host daemon a value it could not
// validate."* Tolerating an unknown key inside a declaration would mean core
// validates a value against a constraint it only half understood and then writes it
// into a file a host daemon reads — a newer manifest declaring
// `{"type": "string", "enum": ["a","b"]}` would have its enum silently dropped and
// core would accept anything. The safe reading of a declaration this build cannot
// decode is "refuse", not "accept the parts I recognised".
//
// The blast radius of that refusal is the same one the retired-key refusal in walk()
// already accepts and is bounded the same way: the manifest fails to load, so
// loadFromDir warns and the loophole VANISHES. No loophole means no daemon and no
// values — fail-closed in every direction, which is what makes a refusal affordable
// here.
func parseSettings(manifestPath string, raw any) ([]Setting, error) {
	if raw == nil {
		return nil, nil
	}
	block, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return nil, Errorf("%s: 'settings' must be a mapping of setting name -> declaration", manifestPath)
	}
	out := []Setting{}
	for _, key := range block.Keys() {
		path := keySettings + "." + key
		if !settingKeyRe.MatchString(key) {
			return nil, Errorf(
				"%s: setting name %s must match %s — a settings key becomes a key in the"+
					" JSON file yolo writes for this loophole, so it is bounded on purpose",
				manifestPath, pytext.Repr(key), pytext.Repr(settingKeyRe.String()))
		}
		declV, _ := block.Get(key)
		decl, isMap := declV.(*jsonx.OrderedMap)
		if !isMap {
			return nil, Errorf(
				"%s: '%s' must be a declaration object, e.g."+
					` {"type": "string_list", "scope": "user", "default": []}`,
				manifestPath, path)
		}
		for _, k := range decl.Keys() {
			if inList(k, settingDeclKeys) {
				continue
			}
			return nil, Errorf(
				"%s: '%s.%s' is not part of the settings declaration schema, and an"+
					" unrecognized declaration key is REFUSED rather than ignored: yolo"+
					" validates config values against this declaration and then writes them"+
					" into a file a host daemon reads, so honoring the half it understood"+
					" would admit a value the other half was meant to reject"+
					" (docs/design/pack-config-keys.md OQ-K1). Known here: %s",
				manifestPath, path, k, strings.Join(sortedCopy(settingDeclKeys), ", "))
		}

		typeV, hasType := decl.Get(keyType)
		typ, typeIsStr := typeV.(string)
		if !hasType || !typeIsStr || typ == "" {
			return nil, Errorf(
				"%s: '%s.type' is required and must be one of %s — an untyped declaration is"+
					" one yolo cannot validate a value against",
				manifestPath, path, sortedListRepr(validSettingTypes))
		}
		if !inList(typ, validSettingTypes) {
			return nil, Errorf("%s: '%s.type'=%s not in %s",
				manifestPath, path, pytext.Repr(typ), sortedListRepr(validSettingTypes))
		}

		scope := DefaultSettingScope
		if sv, ok := decl.Get(keyScope); ok && sv != nil {
			s, isStr := sv.(string)
			if !isStr {
				return nil, Errorf("%s: '%s.scope' must be a string", manifestPath, path)
			}
			scope = s
		}
		if !inList(scope, validSettingScopes) {
			return nil, Errorf(
				"%s: '%s.scope'=%s not in %s — %s means only the user config may supply a"+
					" value (the default when the key is absent), %s means a workspace"+
					" yolo-jail.jsonc may too",
				manifestPath, path, pytext.Repr(scope), sortedListRepr(validSettingScopes),
				pytext.Repr(SettingScopeUser), pytext.Repr(SettingScopeWorkspace))
		}

		description := ""
		if dv, ok := decl.Get(keyDescription); ok && dv != nil {
			s, isStr := dv.(string)
			if !isStr {
				return nil, Errorf("%s: '%s.description' must be a string", manifestPath, path)
			}
			description = s
		}

		def, defSet := SettingZero(typ), false
		if dv, ok := decl.Get(keyDefault); ok && dv != nil {
			coerced, typeErr := CoerceSettingValue(typ, dv)
			if typeErr != "" {
				return nil, Errorf("%s: '%s.default' %s", manifestPath, path, typeErr)
			}
			def, defSet = coerced, true
		}

		out = append(out, Setting{
			Key:         key,
			Type:        typ,
			Scope:       scope,
			Description: description,
			Default:     def,
			DefaultSet:  defSet,
		})
	}
	return out, nil
}

// SettingZero is the value a declared key holds when nothing supplies one and the
// manifest declared no `default`. See Setting.Default for why absence resolves to a
// value rather than to null.
func SettingZero(typ string) any {
	switch typ {
	case SettingTypeBool:
		return false
	case SettingTypeInt:
		return 0
	case SettingTypeStringList:
		return []string{}
	default:
		return ""
	}
}

// CoerceSettingValue type-checks a DECODED JSON value against a declared setting
// type and returns it in the Go shape the type implies. The second return is ""
// on success and otherwise a message fragment reading "expected …, got …", so both
// callers — this package's `default` check and internal/config's check of a
// user-supplied value — report the same mismatch in the same words.
//
// TYPE-CHECKED, NEVER COERCED WITH Truthy/Str, and the reason is the one
// `default_enabled` and `host_daemon.preamble` already state: Truthy("false") is
// true, so a quoted "false" would read as ON — the direction that GRANTS. Every
// type here refuses a value of the wrong shape rather than making one up.
func CoerceSettingValue(typ string, v any) (any, string) {
	switch typ {
	case SettingTypeString:
		s, ok := v.(string)
		if !ok {
			return nil, "expected a string, got " + pytext.Repr(Str(v))
		}
		return s, ""
	case SettingTypeBool:
		b, ok := v.(bool)
		if !ok {
			return nil, "expected a boolean — write true or false (not " + pytext.Repr(Str(v)) + ")"
		}
		return b, ""
	case SettingTypeInt:
		lit, ok := jsonx.AsIntLiteral(v)
		if !ok {
			return nil, "expected an integer, got " + pytext.Repr(Str(v))
		}
		return atoiOr(lit, 0), ""
	case SettingTypeStringList:
		list, ok := v.([]any)
		if !ok || !AllStrings(list) {
			return nil, "expected a list of strings, got " + pytext.Repr(Str(v))
		}
		return StringSlice(list), ""
	}
	// Unreachable: every caller checked the type against validSettingTypes first.
	return nil, "has an unknown declared type " + pytext.Repr(typ)
}

// SettingTypes returns the closed type set, sorted. Exported so a validator in
// another package can name the alternatives without respelling them.
func SettingTypes() []string { return sortedCopy(validSettingTypes) }

// SettingScopes returns the scope vocabulary, sorted.
func SettingScopes() []string { return sortedCopy(validSettingScopes) }
