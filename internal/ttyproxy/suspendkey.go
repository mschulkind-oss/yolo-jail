//go:build linux

package ttyproxy

// suspendkey.go decides what counts as "the user pressed Ctrl-Z" on the host
// terminal. It is a separate file from the pump loop because the answer stopped
// being "one byte" — see the WEDGE OF 2026-09-10 below.
//
// The proxy's whole reason to exist is that this keypress must never reach the
// application inside the jail (docs/reference/ctrl-z-and-the-tty-proxy.md).
// Matching it is therefore a security-of-the-session concern rather than a
// convenience: every encoding this misses is a live wedge.

// Modifier encoding, shared by both escape forms below. A terminal reports
// modifiers as 1 + a bitmask, whose low bits are shift, alt, ctrl, super,
// hyper and meta in that order, and whose top two are the lock states.
const (
	modBase = 1
	modCtrl = 1 << 2

	// The LOCK bits are reported alongside the real modifiers and say nothing
	// about what the user pressed. Ctrl-Z with Num Lock on arrives as 133, not
	// 5, and a matcher that demands exactly 5 silently stops working for anyone
	// who leaves a lock key on — a bug that would look like "it works on my
	// machine" forever.
	modCapsLock = 1 << 6
	modNumLock  = 1 << 7
	lockMods    = modCapsLock | modNumLock

	// realMods is every bit the field can carry EXCEPT the locks — defined by
	// subtraction rather than by listing shift/alt/super/hyper/meta, so a
	// modifier bit that does not exist yet disqualifies a match instead of
	// being silently ignored. Conservative is the right default here: the cost
	// of not matching is a keypress the agent handles, and the cost of matching
	// too eagerly is a keypress stolen from it.
	realMods = 0xff &^ lockMods
)

// Key event types in the kitty protocol's optional `:event` subparameter.
const (
	eventPress   = 1
	eventRepeat  = 2
	eventRelease = 3
)

const (
	esc                  = 0x1b
	keyZ                 = 122 // 'z', the unicode code point both escape forms carry
	xtermOtherKeysPrefix = 27
)

// isCtrlOnly reports whether a reported modifier value means Ctrl and nothing
// else. Lock state is masked off (see modCapsLock); any other real modifier
// disqualifies, so Ctrl-Shift-Z and Ctrl-Alt-Z pass through to the application
// as the distinct keys they are.
func isCtrlOnly(mods int) bool {
	if mods < modBase {
		return false
	}
	return (mods-modBase)&realMods == modCtrl
}

// isSuspendEvent reports whether a key event type should suspend. Press and
// repeat do; RELEASE MUST NOT. A terminal in "report all keys" mode sends both
// a press and a release for one keypress, and suspending on both would stop
// the proxy a second time the instant the user resumed it.
func isSuspendEvent(event int) bool {
	return event == eventPress || event == eventRepeat
}

// findSuspendKey locates a Ctrl-Z keypress in data and returns the half-open
// byte range [start, end) to strip before the rest is forwarded to the child.
//
// THE WEDGE OF 2026-09-10. This was `indexByte(data, 0x1a)` for the proxy's
// whole life, and that is only the LEGACY encoding. A terminal that has been
// asked for the kitty keyboard protocol stops sending control bytes and sends
// escape sequences instead, so Ctrl-Z arrives as `ESC [ 122 ; 5 u` — which
// holds no 0x1A anywhere. The byte scan forwarded it verbatim, the agent
// decoded it, called kill(0, SIGTSTP), and stopped its whole process group
// inside the container: measured on a real host, five processes in state T,
// exactly the wedge this package was written to prevent, walking straight
// through the filter meant to stop it.
//
// Both escape forms are handled, because the choice belongs to the terminal
// and the application, not to us: the kitty protocol's CSI-u form and xterm's
// older modifyOtherKeys form.
//
// LIMITATION, deliberate: a sequence SPLIT ACROSS TWO READS is not matched, and
// passes through. A keypress reaches us as one write from the terminal and one
// read from the pty, so this does not happen in practice. Holding back a
// partial tail to cover it would mean withholding real input on a timer, which
// is a worse failure than the one it prevents — add it if a split is ever
// actually observed, not before.
func findSuspendKey(data []byte) (start, end int, ok bool) {
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case suspByte:
			return i, i + 1, true
		case esc:
			if n, matched := matchEscapedSuspend(data[i:]); matched {
				return i, i + n, true
			}
		}
	}
	return 0, 0, false
}

// matchEscapedSuspend reports whether b BEGINS with an escape-encoded Ctrl-Z,
// and how many bytes it occupies. b[0] is known to be ESC.
func matchEscapedSuspend(b []byte) (int, bool) {
	params, final, n, ok := parseCSI(b)
	if !ok {
		return 0, false
	}
	switch final {
	case 'u':
		// The kitty keyboard protocol: CSI <code> ; <mods>[:<event>] u.
		// A bare `CSI 122 u` is an unmodified 'z' reported as an escape code —
		// no modifier field, so not our key.
		if len(params) < 2 || atoiField(params[0]) != keyZ {
			return 0, false
		}
		mods, event := parseModsAndEvent(params[1])
		if !isCtrlOnly(mods) || !isSuspendEvent(event) {
			return 0, false
		}
		return n, true
	case '~':
		// xterm modifyOtherKeys: CSI 27 ; <mods> ; <code> ~.
		if len(params) < 3 || atoiField(params[0]) != xtermOtherKeysPrefix ||
			atoiField(params[2]) != keyZ {
			return 0, false
		}
		if !isCtrlOnly(atoiField(params[1])) {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// parseCSI splits a CSI sequence at the front of b into its ';'-separated
// parameter fields (subparameters left intact) and its final byte, returning
// the total length consumed. An incomplete or malformed sequence is not ok.
func parseCSI(b []byte) (params []string, final byte, n int, ok bool) {
	if len(b) < 3 || b[0] != esc || b[1] != '[' {
		return nil, 0, 0, false
	}
	i := 2
	for ; i < len(b); i++ {
		c := b[i]
		if c >= '0' && c <= '9' || c == ';' || c == ':' {
			continue
		}
		// A final byte ends the sequence; anything else (an intermediate, a
		// private marker like '?') is not a shape we claim.
		if c >= 0x40 && c <= 0x7e {
			return splitFields(b[2:i]), c, i + 1, true
		}
		return nil, 0, 0, false
	}
	return nil, 0, 0, false // ran out of bytes: incomplete, see LIMITATION
}

// splitFields splits CSI parameters on ';'. Empty input yields no fields, so a
// bare `CSI u` cannot be mistaken for a one-field sequence.
func splitFields(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(b); i++ {
		if i == len(b) || b[i] == ';' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	return out
}

// parseModsAndEvent reads a `<mods>` or `<mods>:<event>` field. An absent event
// subparameter means a press, which is the protocol's own default.
func parseModsAndEvent(field string) (mods, event int) {
	event = eventPress
	for i := 0; i < len(field); i++ {
		if field[i] == ':' {
			return atoiField(field[:i]), atoiField(field[i+1:])
		}
	}
	return atoiField(field), event
}

// atoiField parses a decimal parameter field. -1 for anything that is not a
// plain number, so a malformed field can never accidentally equal a real one.
func atoiField(s string) int {
	if s == "" {
		return -1
	}
	v := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return -1
		}
		v = v*10 + int(s[i]-'0')
		if v > 1<<20 {
			return -1 // a parameter this large is not a key we know
		}
	}
	return v
}
