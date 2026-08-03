package entrypoint

// surfacecodec.go is the CODEC BOUNDARY OF THE RMW MECHANISM — the read and the write
// halves of `mode: rmw`, dispatched on the surface's declared codec.
//
// It exists because the RMW path had no codec dispatch at all. It read every surface with
// loadObject (JSON-only, empty-on-failure) and wrote every surface with dumpJSONIndent2,
// which is correct for the two JSON surfaces the JAIL renders in this mode
// (claude/config, copilot/config) and destructive for anything else. At the HOST notch
// that stopped being an edge case: `apply --host` is pure RMW for EVERY surface
// (host-render-target.md §6.3), so codex/config — `codec: "toml"` — was read as JSON
// (unparseable => empty object, so every key the user owned vanished) and then written
// back as JSON. A valid config.toml went in and a JSON file with three yolo keys came out.
//
// Two rules, and the second is the one that turns data loss into a safe no-op:
//
//   - THE CODEC DECIDES both ends. Decode with the surface's codec, encode with the same
//     one. The TOML side reuses internal/tomlx (decode, order-preserving) and
//     internal/agentcfg/codec (encode) — the same two the jail's compose path uses, so
//     there is exactly one TOML emitter in the tree.
//   - AN UNPARSEABLE FILE IS REFUSED, NEVER REWRITTEN. RMW means "preserve everything yolo
//     does not declare"; a read that cannot see the existing keys cannot honor that, so
//     starting from an empty object is not a degraded render, it is deletion. Refusing
//     leaves the file byte-for-byte untouched and says why.
//
// Keyless codecs (`lines`, `raw`) are refused outright rather than approximated: RMW's
// whole vocabulary is "assert these keys, fill those, keep the rest", and a surface whose
// decoded value is a list or a string has no keys to assert. See rmwCodecRefusal.

import (
	"bytes"
	"fmt"
	"os"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// rmwRefusedError is a DELIBERATE non-write: the RMW mechanism declined to touch a file
// because it could not guarantee the round-trip. It is distinguished from an ordinary error
// so each caller can report it in its own vocabulary — the host as a per-surface
// `refused: …` line, the jail as a boot warning — instead of both inheriting one of the
// two wrong outcomes (a silent skip, or a fatal boot for a file yolo left intact).
type rmwRefusedError struct {
	surface string // "agent/name"
	reason  string
}

func (e *rmwRefusedError) Error() string { return e.surface + ": refused: " + e.reason }

// Reason is the bare explanation, for a caller that supplies its own prefix.
func (e *rmwRefusedError) Reason() string { return e.reason }

// asRMWRefusal reports whether err is a refusal (and returns it), so a caller can branch
// on "yolo chose not to write" versus "the write failed".
func asRMWRefusal(err error) (*rmwRefusedError, bool) {
	var r *rmwRefusedError
	if err == nil {
		return nil, false
	}
	if e, ok := err.(*rmwRefusedError); ok {
		r = e
		return r, true
	}
	return nil, false
}

// refuseRMW builds a refusal for one surface.
func refuseRMW(surface manifest.Surface, format string, args ...any) *rmwRefusedError {
	return &rmwRefusedError{
		surface: surface.Agent + "/" + surface.Name,
		reason:  fmt.Sprintf(format, args...),
	}
}

// rmwCodecRefusal returns a refusal when the surface's codec cannot participate in RMW at
// all, or nil when it can.
//
// The gate is the codec's KIND, not a name list: RMW asserts and fills individual keys, so
// it needs an object. codec.KindArray (`lines`) and codec.KindScalar (`raw`) have exactly
// one "key" — the whole file — which means the only RMW an honest implementation could do
// is replace the file wholesale, i.e. the opposite of the mode's promise. Those surfaces
// belong in `stateful`/`computed`, where whole-file replacement is the declared behavior
// and the capture sidecars carry the user's edits.
//
// An UNKNOWN codec is refused too. manifest validation rejects one, so this is unreachable
// through a validated surface — but it is the difference between a refusal and a nil-codec
// panic if that ever stops being true.
func rmwCodecRefusal(surface manifest.Surface) *rmwRefusedError {
	if _, known := codec.LookupCodec(surface.Codec); !known {
		return refuseRMW(surface, "unknown codec %q (want one of %v)",
			surface.Codec, manifest.CodecNames())
	}
	if surface.Kind() != codec.KindObject {
		return refuseRMW(surface, "RMW for codec %q is not implemented at this notch — "+
			"%s has no keys to assert (its decoded value is the whole file), so a "+
			"read-modify-write could only replace it wholesale; declare this surface "+
			"`stateful` or `computed` instead", surface.Codec, surface.Codec)
	}
	return nil
}

// decodeSurfaceObject reads path with the SURFACE'S codec and returns its top-level object.
//
// Three outcomes, and the difference between the first two is the whole point:
//
//	absent / empty file  => an empty object, no error. Nothing to preserve, so every key
//	                        this render writes is an ADD. This is the ordinary first-apply
//	                        case and must not refuse.
//	present but garbage  => a REFUSAL. The keys are there and yolo cannot see them; writing
//	                        would delete them. (This includes a valid document whose top
//	                        level is not an object — a JSON array, say — which has no keys
//	                        to merge into.)
//	present and valid    => the decoded object, key order preserved.
//
// Order preservation is why this does not just call the codec's own Decode: codec.JSON and
// codec.TOML both yield plain map[string]any, and the RMW writer's contract with a JSON
// surface is that the user's key order survives (dumpJSONIndent2 over an insertion-ordered
// map). jsonx.Decode and tomlx.DecodeOrdered are the order-preserving decoders for the two
// object codecs; the ENCODE side still goes through the shared codec (see
// encodeSurfaceObject), so there is no second parser or second emitter here.
func decodeSurfaceObject(surface manifest.Surface, path string) (*jsonx.OrderedMap, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonx.NewOrderedMap(), nil
		}
		// An existing-but-unreadable file (permissions, a directory in the way) is not a
		// blank slate either. Refuse rather than write over what we could not read.
		return nil, refuseRMW(surface, "cannot read %s: %v — the file is left untouched",
			path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return jsonx.NewOrderedMap(), nil
	}
	switch surface.Codec {
	case "json":
		decoded, derr := jsonx.Decode(raw)
		if derr != nil {
			return nil, refuseRMW(surface, "%s is not valid JSON (%v) — refusing to "+
				"rewrite it, because a read that cannot see your keys cannot preserve "+
				"them; fix or move the file and re-run", path, derr)
		}
		m, isObj := decoded.(*jsonx.OrderedMap)
		if !isObj {
			return nil, refuseRMW(surface, "%s is valid JSON but not an object, so there "+
				"are no keys to merge into — refusing to replace it", path)
		}
		return m, nil
	case "toml":
		m, derr := tomlx.DecodeOrdered(raw)
		if derr != nil {
			return nil, refuseRMW(surface, "%s is not valid TOML (%v) — refusing to "+
				"rewrite it, because a read that cannot see your keys cannot preserve "+
				"them; fix or move the file and re-run", path, derr)
		}
		return m, nil
	default:
		// Unreachable: rmwCodecRefusal has already rejected every non-object codec, and
		// json/toml are the only two. Kept as a refusal rather than a fallthrough to JSON
		// so a codec added tomorrow fails closed instead of being silently mis-parsed.
		return nil, refuseRMW(surface, "no RMW decoder for codec %q", surface.Codec)
	}
}

// encodeSurfaceObject renders obj as the exact file text to write, using the surface's
// codec. It returns a refusal when the value cannot be encoded, so an unencodable render
// leaves the file alone instead of truncating it.
//
// JSON goes through dumpJSONIndent2 — byte-for-byte what this path has always written, so
// every JSON surface (every RMW surface the jail renders) is unchanged.
//
// TOML goes through internal/agentcfg/codec's emitter, the same one the jail's compose path
// uses, deliberately rather than a second one local to the RMW writer. Two consequences the
// caller has to own, neither of which this function can hide:
//
//   - COMMENTS AND KEY ORDER ARE NOT PRESERVED. The emitter is a canonical, deterministic
//     renderer (sorted keys, no comments) — see codec.Codec's round-trip contract, and
//     BACKLOG E4 for comment preservation as tracked, deliberately-unbuilt work. So the
//     VALUES round-trip and the FORMATTING does not. Callers report the comment loss
//     (tomlHasComments); this function does not warn, because a pure encoder that printed
//     to stderr would be unreportable in the observe posture that needs it most.
//   - it is deterministic, so a second apply writes byte-identical bytes.
func encodeSurfaceObject(surface manifest.Surface, obj *jsonx.OrderedMap) (string, error) {
	switch surface.Codec {
	case "json":
		return dumpJSONIndent2(obj), nil
	case "toml":
		c, _ := codec.LookupCodec("toml")
		encoded, err := c.Encode(tomlValue(obj))
		if err != nil {
			return "", refuseRMW(surface, "the composed value cannot be written as TOML "+
				"(%v) — the file is left untouched", err)
		}
		return string(encoded) + "\n", nil
	default:
		return "", refuseRMW(surface, "no RMW encoder for codec %q", surface.Codec)
	}
}

// tomlValue lowers the RMW writer's jsonx value model into the generic model the TOML
// emitter takes. It is NOT jsonx.Plain, and the two differences are both correctness:
//
//   - INTEGERS STAY INTEGERS. jsonx.Plain turns an integer literal into float64 for the
//     benefit of encoding/json; the TOML emitter would then render it `4096.0`, silently
//     retyping a user's `model_max_output_tokens` on every apply. Here an integer literal
//     becomes int64, which the emitter writes back as a bare integer.
//   - NIL LEAVES ARE DROPPED. TOML has no null, so codec.TOML refuses a nil value — which
//     would refuse the WHOLE surface over one RFC-7386 tombstone. Dropping the key is what
//     a tombstone means anyway ("this key is not present"), so it is the faithful rendering
//     rather than a workaround.
//
// Values that arrive from a TOML decode (int64, float64, bool, string, time.Time) pass
// through; the emitter has the final say on what it can render, and anything it cannot
// becomes a refusal at the call site rather than a mangled file.
func tomlValue(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		out := make(map[string]any, t.Len())
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			if val == nil {
				continue // no null in TOML; a tombstone means "absent"
			}
			out[k] = tomlValue(val)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if val == nil {
				continue
			}
			out[k] = tomlValue(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, tomlValue(e))
		}
		return out
	default:
		if lit, isInt := jsonx.AsIntLiteral(v); isInt {
			if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
				return n
			}
			// A literal past int64 (jsonx accepts arbitrary-precision decimals) has no TOML
			// integer representation. Hand it to the emitter as-is so it refuses by type
			// rather than being silently truncated here.
			return v
		}
		return v
	}
}

// tomlHasComments reports whether TOML bytes carry a comment, so a caller can WARN that a
// render drops them.
//
// The warning is the honest half of reusing the canonical emitter: comment preservation is
// BACKLOG E4 (open, deliberately unbuilt), so a host apply on a commented config.toml keeps
// every value and loses every comment. That is a real loss to the user even though nothing
// they configured changed, and the never-silent discipline says name it.
//
// It is quote-aware rather than a bare bytes.Contains("#"), because a `#` inside a string
// value (`url = "https://x/#frag"`) is not a comment and a spurious warning on every apply
// is how a warning stops being read. Handles basic, literal, and both multi-line string
// forms; anything else it sees as a top-level `#` is a comment.
func tomlHasComments(data []byte) bool {
	for i := 0; i < len(data); {
		switch {
		case data[i] == '#':
			return true
		case bytes.HasPrefix(data[i:], []byte(`"""`)):
			i = skipDelimited(data, i+3, `"""`)
		case bytes.HasPrefix(data[i:], []byte("'''")):
			i = skipDelimited(data, i+3, "'''")
		case data[i] == '"':
			i = skipBasicString(data, i+1)
		case data[i] == '\'':
			// A literal string has no escapes and cannot span a line.
			i = skipToAny(data, i+1, '\'', '\n')
		default:
			i++
		}
	}
	return false
}

// skipDelimited returns the index just past the next occurrence of close, or len(data) when
// the delimiter is unterminated (a malformed file — the decoder will refuse it anyway).
func skipDelimited(data []byte, from int, close string) int {
	if from >= len(data) {
		return len(data)
	}
	if j := bytes.Index(data[from:], []byte(close)); j >= 0 {
		return from + j + len(close)
	}
	return len(data)
}

// skipBasicString returns the index just past the closing quote of a basic string, honoring
// backslash escapes. An unterminated string ends at the newline (TOML forbids a raw newline
// in a basic string) or at EOF.
func skipBasicString(data []byte, from int) int {
	for i := from; i < len(data); i++ {
		switch data[i] {
		case '\\':
			i++ // skip the escaped byte
		case '"':
			return i + 1
		case '\n':
			return i + 1
		}
	}
	return len(data)
}

// skipToAny returns the index just past the first occurrence of any of the given bytes.
func skipToAny(data []byte, from int, stops ...byte) int {
	for i := from; i < len(data); i++ {
		for _, s := range stops {
			if data[i] == s {
				return i + 1
			}
		}
	}
	return len(data)
}
