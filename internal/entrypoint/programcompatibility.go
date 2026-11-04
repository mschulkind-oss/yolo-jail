package entrypoint

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const programCompatibilityDir = ".config/yolo-program-compatibility"

// selectedProgramCompatibility reads a constraint only from a files contribution declared by
// the pack that owns this installed bin. The generated launcher bakes these pack-tree bytes;
// it never trusts the mutable home file to which the generic files surface may also deliver them.
func selectedProgramCompatibility(p *packload.Pack, inst *packdecl.Install) (*packdecl.VersionCompatibility, error) {
	if p == nil || inst == nil || !packdecl.ValidBinName(inst.Bin) {
		return nil, nil
	}
	into := path.Join(programCompatibilityDir, inst.Bin+".json")
	var declaration *packdecl.Contribution
	for _, c := range p.Decl.Contributions() {
		if c.Kind != packdecl.KindFiles || c.Into != into {
			continue
		}
		if declaration != nil {
			return nil, fmt.Errorf("pack %s: duplicate program compatibility files contributions for %s — remove the duplicate declaration and restart", p.Name, inst.Bin)
		}
		copy := c
		declaration = &copy
	}
	if declaration == nil {
		return nil, nil
	}
	if inst.Kind != "native" {
		return nil, fmt.Errorf("pack %s: compatibility data for %s requires a native installer program — use a native installer or remove this compatibility files contribution, then restart", p.Name, inst.Bin)
	}
	if !fs.ValidPath(declaration.From) || declaration.From == "." {
		return nil, fmt.Errorf("pack %s: invalid compatibility source path %q — correct the files contribution's from path and restart", p.Name, declaration.From)
	}
	root, err := os.OpenRoot(p.Root)
	if err != nil {
		return nil, fmt.Errorf("pack %s: open compatibility source root: %w — repair the selected pack tree and restart", p.Name, err)
	}
	defer root.Close()
	file, err := root.Open(declaration.From)
	if err != nil {
		return nil, fmt.Errorf("pack %s: open compatibility source %q: %w — restore the declared source file and restart", p.Name, declaration.From, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("pack %s: stat compatibility source %q: %w — restore a regular declared source file and restart", p.Name, declaration.From, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("pack %s: compatibility source %q is not a regular file — select a regular JSON file in the files contribution and restart", p.Name, declaration.From)
	}
	const maxCompatibilityBytes = 4096
	data, err := io.ReadAll(io.LimitReader(file, maxCompatibilityBytes+1))
	if err != nil {
		return nil, fmt.Errorf("pack %s: read compatibility source %q: %w — repair the declared JSON file and restart", p.Name, declaration.From, err)
	}
	if len(data) > maxCompatibilityBytes {
		return nil, fmt.Errorf("pack %s: compatibility source %q exceeds %d bytes — reduce it to a small JSON constraint and restart", p.Name, declaration.From, maxCompatibilityBytes)
	}
	compatibility, problems := packdecl.DecodeVersionCompatibility(data, inst.ProbeArgs)
	if len(problems) > 0 {
		return nil, fmt.Errorf("pack %s: %s — correct the declared compatibility JSON and restart", p.Name, strings.Join(problems, "; "))
	}
	return compatibility, nil
}

// nativeCompatibilityShell emits a version refusal only for the self mode carried by the
// host-resolved native launch context. Container launchers and explicit account mode keep their
// existing behavior; invalid native metadata cannot silently disable a declared compatibility gate.
//
// PARITY: the gate is not a per-backend difference in behavior but the shadow of one. It exists
// for Copilot 1.0.93+ (yolo-jail #65, github/copilot-cli#5085), whose native TLS needs the Mach
// service com.apple.SecurityServer, and only run-as-you mode's Seatbelt profile denies that
// service, on purpose, as the way into the user's login keychain (macos-native-tls.md MT-D7). A
// container runs the Linux Copilot, which reads a CA bundle, and the dedicated-account mode leaves
// the service reachable, so neither meets the failure and neither is gated.
func nativeCompatibilityShell(compatibility *packdecl.VersionCompatibility, context string, native bool) string {
	if compatibility == nil || !native {
		return "COMPATIBILITY_CONTEXT_VALID=1\nCOMPATIBILITY_ENABLED=0\n" + nativeCompatibilityFunctions
	}
	valid, self := nativeCompatibilityMode(context)
	if !valid {
		return "COMPATIBILITY_CONTEXT_VALID=0\nCOMPATIBILITY_ENABLED=0\n" +
			"COMPATIBILITY_MESSAGE=" + shquote.Quote("yolo: ERROR: invalid macos-user launch context; refusing this program because its native compatibility rule cannot be evaluated. This is yolo's to fix; run it as the yolo account: yolo --setup seatbelt-yolo-account -- <cmd>, or in a container: yolo --setup podman-self -- <cmd> (apple-container-self for Apple Container).") + "\n" + nativeCompatibilityFunctions
	}
	if !self {
		return "COMPATIBILITY_CONTEXT_VALID=1\nCOMPATIBILITY_ENABLED=0\n" + nativeCompatibilityFunctions
	}
	return "COMPATIBILITY_CONTEXT_VALID=1\nCOMPATIBILITY_ENABLED=1\n" +
		"COMPATIBILITY_REFUSE_AT_OR_ABOVE=" + shquote.Quote(compatibility.RefuseAtOrAbove) + "\n" +
		"COMPATIBILITY_MESSAGE=" + shquote.Quote(compatibility.Message) + "\n" + nativeCompatibilityFunctions
}

// The context is emitted from the resolved macos-user session plan, not read from an agent's
// environment. A mode is valid only when it is explicitly encoded as account (0) or self (1).
func nativeCompatibilityMode(context string) (valid, self bool) {
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal([]byte(context), &metadata); err != nil || metadata == nil {
		return false, false
	}
	mode, ok := metadata["mode"]
	if !ok {
		return false, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(string(mode)))
	if err != nil {
		return false, false
	}
	switch value {
	case 0:
		return true, false
	case 1:
		return true, true
	default:
		return false, false
	}
}

// nativeCompatibilityFunctions is the gate's shell. Every refusal it makes in self mode ends with
// _yolo_self_mode_next_steps, the routes that run the program (the Happy Path Principle): the
// dedicated-account mode, and a container backend for one launch or for good. They are typed
// outside the jail, as the user config they edit is the host's. Nothing here offers
// YOLO_USE_INSTALLED: a refused version fails the same way however it is started, and the gate runs
// before the update act, so no refusal of a failed update and its "Start anyway:" is reached.
const nativeCompatibilityFunctions = `
_yolo_self_mode_next_steps() {
    {
        echo
        echo "Next steps, in a terminal outside this jail:"
        printf '  %-48s  %s\n' \
            "yolo config set setup seatbelt-yolo-account --user" "run as the yolo account (yolo yolo-account create creates it once)" \
            "yolo --setup podman-self -- $BIN" "run this launch in a podman container (apple-container-self for Apple Container)" \
            "yolo config set setup podman-self --user" "run every launch in a podman container (apple-container-self for Apple Container)"
    } >&2
}

_yolo_version_at_least() {
    local IFS=. actual_major actual_minor actual_patch threshold_major threshold_minor threshold_patch
    read -r actual_major actual_minor actual_patch <<< "$1"
    read -r threshold_major threshold_minor threshold_patch <<< "$2"
    if (( 10#$actual_major > 10#$threshold_major )); then return 0; fi
    if (( 10#$actual_major < 10#$threshold_major )); then return 1; fi
    if (( 10#$actual_minor > 10#$threshold_minor )); then return 0; fi
    if (( 10#$actual_minor < 10#$threshold_minor )); then return 1; fi
    (( 10#$actual_patch >= 10#$threshold_patch ))
}

_yolo_compatibility_check() {
    [ "${_YOLO_PROBE:-}" = "1" ] && return 0
    if [ "$COMPATIBILITY_CONTEXT_VALID" != "1" ]; then
        printf '%s\n' "$COMPATIBILITY_MESSAGE" >&2
        return 1
    fi
    [ "$COMPATIBILITY_ENABLED" = "1" ] || return 0
    [ -x "$REAL_BIN" ] || return 0
    local output actual probe_arg probe_ok=0
    for probe_arg in "${PROBE_ARGS[@]}"; do
        if output=$("$REAL_BIN" "$probe_arg" 2>&1); then
            probe_ok=1
            break
        fi
    done
    if [ "$probe_ok" != "1" ]; then
        echo "yolo: ERROR: cannot verify $BIN's version for this macos-user self launch; use a container backend (podman or container), or repair its declared version probe." >&2
        _yolo_self_mode_next_steps
        return 1
    fi
    if [[ "$output" =~ (^|[^[:alnum:].]|[vV])([0-9]+)\.([0-9]+)\.([0-9]+)($|[^[:alnum:].]|\.($|[[:space:]])) ]]; then
        actual="${BASH_REMATCH[2]}.${BASH_REMATCH[3]}.${BASH_REMATCH[4]}"
    else
        echo "yolo: ERROR: cannot parse $BIN's declared version probe for this macos-user self launch; use a container backend (podman or container), or repair its declared version probe." >&2
        _yolo_self_mode_next_steps
        return 1
    fi
    if [ "${#BASH_REMATCH[2]}" -gt 9 ] || [ "${#BASH_REMATCH[3]}" -gt 9 ] || [ "${#BASH_REMATCH[4]}" -gt 9 ]; then
        echo "yolo: ERROR: $BIN's version is outside the supported numeric comparison bound; refusing this macos-user self launch. Use a container backend (podman or container)." >&2
        _yolo_self_mode_next_steps
        return 1
    fi
    if _yolo_version_at_least "$actual" "$COMPATIBILITY_REFUSE_AT_OR_ABOVE"; then
        printf 'yolo: ERROR: %s %s is installed. %s\n' "$BIN" "$actual" "$COMPATIBILITY_MESSAGE" >&2
        _yolo_self_mode_next_steps
        return 1
    fi
    return 0
}
`
