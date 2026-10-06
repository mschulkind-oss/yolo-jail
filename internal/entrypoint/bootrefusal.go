package entrypoint

// bootrefusal.go is a refused boot's STRUCTURED CAUSE: for each config generator that failed, what
// it was doing in plain words, the pack whose contribution it was, the path its error names and
// whether that path was read-only, written beside boot.log as <workspace>/.yolo/boot-refusal.json
// (docs/design/patched-extensions.md PPX-D42).
//
// # Who reads it
//
// The host's build act (internal/cli's forkbuild.go). A fork's or a patched extension's build jail
// that refuses at its boot leaves the host only its printed lines, and the last of those were
// what a launch's user cannot act on: the hold offer ("Re-run with YOLO_HOLD_ON_REFUSAL=1 …"),
// then the refusal, whose items name a generator by its internal step ("configure_pi_automode")
// rather than the pack and the file. The maintainer's first launch with patched extensions relayed
// exactly that, joined into one line, four times. So the boot that refuses says what it means in a
// form the host reads, rather than the host parsing the prose meant for a terminal; the prose is
// unchanged.
//
// # What it is not
//
// Never a gate. Nothing decides whether to start a jail from it, a boot that cannot write it
// refuses exactly as before, and a boot that succeeds removes the last one (clearBootRefusal), so a
// record on disk always describes the latest refused boot of the workspace. It lives in a directory
// the jail writes, so its reader treats every string in it as the jail's words, never as markup
// (ReadBootRefusal strips control characters and bounds it).

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// BootRefusalName is the record's file name in the workspace's state directory.
const BootRefusalName = "boot-refusal.json"

// bootRefusalMax bounds what ReadBootRefusal reads: a refusal's record is a few hundred bytes.
const bootRefusalMax = 64 << 10

// BootRefusal is one refused boot's record.
type BootRefusal struct {
	// Failures are the generators that failed, in the order they ran.
	Failures []GenFailure `json:"failures"`
}

// GenFailure is one failed config generator, as BootRefusal records it.
type GenFailure struct {
	// Step is the generator's own name, as the refusal and boot.log print it ("configure_pi_automode").
	Step string `json:"step,omitempty"`
	// Doing is what it was doing, in plain words, naming the file it writes when there is one
	// ("writing pi's automode file (~/.pi/agent/extensions/pi-automode/settings.json)"); "" for a
	// step this boot has no words for.
	Doing string `json:"doing,omitempty"`
	// Pack is the pack whose contribution it was, "" for a step of yolo's own.
	Pack string `json:"pack,omitempty"`
	// Path is the path the error names, home-relative ("~/…") under the jail's home; "" when the
	// error names none.
	Path string `json:"path,omitempty"`
	// ReadOnly says the error is the file system refusing a write at Path because it is mounted
	// read-only in this jail (EROFS): a fact about the jail's mounts, which yolo makes, rather than
	// about the pack's content.
	ReadOnly bool `json:"read_only,omitempty"`
	// Error is the generator's error as the refusal prints it.
	Error string `json:"error"`
}

// genAbout is what a generator step is about, for its record: genStepAbout's argument.
type genAbout struct {
	doing, pack string
}

// genFailureOf is err, from the step label running about, as its record.
func genFailureOf(e *Env, label string, about genAbout, err error) GenFailure {
	f := GenFailure{Step: label, Doing: about.doing, Pack: about.pack, Error: err.Error()}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		f.Path = homeRelative(e, pe.Path)
	}
	f.ReadOnly = errors.Is(err, syscall.EROFS)
	return f
}

// homeRelative is p with the jail's home spelled "~", as a pack names its files.
func homeRelative(e *Env, p string) string {
	if home := strings.TrimSuffix(e.Home, "/"); home != "" {
		if p == home {
			return "~"
		}
		if rest, ok := strings.CutPrefix(p, home+"/"); ok {
			return "~/" + rest
		}
	}
	return p
}

// Lines are the failure in plain words, one line each: what was being done and whose it was, then
// what went wrong — a read-only path as that, anything else as the error said it. Two lines when
// there are words for the step, one otherwise; the caller indents them.
func (f GenFailure) Lines() []string {
	what := f.Doing
	if what == "" {
		if f.Step == "" {
			return []string{f.Error}
		}
		what = "the boot step " + f.Step
	}
	if f.Pack != "" {
		what += ", which pack " + f.Pack + " declares,"
	}
	why := f.Error
	if f.ReadOnly && f.Path != "" {
		why = f.Path + " is mounted read-only in that jail"
	}
	return []string{what + " failed:", "  " + why}
}

// recordBootRefusal writes this boot's record, never failing the boot: a record that cannot be
// written leaves the refusal's prose, which the boot prints either way.
func recordBootRefusal(e *Env) {
	if len(e.genRecords) == 0 {
		return
	}
	data, err := json.MarshalIndent(BootRefusal{Failures: e.genRecords}, "", "  ")
	if err != nil {
		return
	}
	f, err := paths.OpenWorkspaceStateFile(e.WorkspaceDir(), BootRefusalName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// clearBootRefusal removes the last refused boot's record, so the one on disk is never a boot
// before this one's.
func clearBootRefusal(e *Env) {
	_ = os.Remove(filepath.Join(paths.WorkspaceStateDir(e.WorkspaceDir()), BootRefusalName))
}

// ReadBootRefusal is the record a refused boot of workspace left, nil when there is none or it does
// not read. Every string in it is the jail's: control characters are dropped and each is bounded,
// so a caller prints it as text and never as markup or terminal control.
func ReadBootRefusal(workspace string) *BootRefusal {
	f, err := paths.OpenWorkspaceStateFile(workspace, BootRefusalName, os.O_RDONLY, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, bootRefusalMax))
	if err != nil {
		return nil
	}
	var r BootRefusal
	if json.Unmarshal(data, &r) != nil || len(r.Failures) == 0 {
		return nil
	}
	for i := range r.Failures {
		g := &r.Failures[i]
		g.Step, g.Doing, g.Pack, g.Path, g.Error = jailText(g.Step), jailText(g.Doing), jailText(g.Pack),
			jailText(g.Path), jailText(g.Error)
	}
	return &r
}

// jailTextMax bounds one string of the record as its reader prints it.
const jailTextMax = 400

// jailText is s as a line a host prints: no control character, at most jailTextMax bytes.
func jailText(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, s)
	if len(s) > jailTextMax {
		s = strings.ToValidUTF8(s[:jailTextMax], "") + "…"
	}
	return s
}

// IsHoldOfferLine reports whether line, as a terminal shows it, is one of the hold offer's lines
// (holdOffer): a step for the person at the refused boot's own terminal, which a relay of what the
// boot said leaves out, since it is not what went wrong.
func IsHoldOfferLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" {
		return false
	}
	for _, l := range strings.Split(holdOffer, "\n") {
		if l = strings.TrimSpace(l); l != "" && line == l {
			return true
		}
	}
	return false
}

// String is the failure as one line, for a record that must be one: its Lines joined.
func (f GenFailure) String() string {
	lines := f.Lines()
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, " ")
}
