package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// This file holds the terminal-facing jail indicators (tmux/kitty) that wrap a
// `yolo` run. It was formerly internal/frontdoor's terminal.go; SetupJailIndicator
// is called only from the cli dispatch (runRun), so it lives here while the
// StartupBanner half moved next to its sole caller in internal/cli/run.

// projectName returns the jail label: $SM_PROJECT or the cwd basename.
func projectName() string {
	if p := os.Getenv("SM_PROJECT"); p != "" {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Base(cwd)
}

// SetupJailIndicator sets the terminal jail indicator (kitty tab or tmux pane
// border) and returns a restore func (or nil). Detection priority mirrors
// main(): KITTY_PID (and not TMUX) -> kitty; else tmux. YOLO_NO_TMUX=1 skips
// tmux. Only call this when NOT delegating (see the package doc).
func SetupJailIndicator() func() {
	if os.Getenv("KITTY_PID") != "" && os.Getenv("TMUX") == "" {
		return kittySetupJailTab()
	}
	return tmuxSetupJailPane()
}

// isattyStdin and tmuxCmd are vars so terminal_test.go can drive the real
// tmuxSetupJailPane without a tmux server. tmuxCmd is the ONE exec seam for every
// tmux call this file makes — a second, unseamed call site would be invisible to
// the restore-argv regressions, which assert on exactly what reaches tmux.
var isattyStdin = func() bool {
	return tty.IsTerminalFile(os.Stdin)
}

var tmuxCmd = func(args ...string) ([]byte, error) {
	return exec.Command("tmux", args...).Output()
}

func kittenRun(args ...string) {
	cmd := exec.Command("kitten", append([]string{"@"}, args...)...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()
}

func kittySetupJailTab() func() {
	if os.Getenv("KITTY_PID") == "" || !isattyStdin() {
		return nil
	}
	project := projectName()
	windowID := os.Getenv("KITTY_WINDOW_ID")
	matchArg := "recent:0"
	if windowID != "" {
		matchArg = "id:" + windowID
	}

	oldTitle := ""
	if out, err := exec.Command("kitten", "@", "get-tab-title", "--match", matchArg).Output(); err == nil {
		oldTitle = strings.TrimSpace(string(out))
	}

	set := exec.Command("kitten", "@", "set-tab-title", "--match", matchArg, "🔒 JAIL "+project)
	set.Stdout, set.Stderr = nil, nil
	if err := set.Run(); err != nil {
		return nil
	}
	kittenRun("set-tab-color", "--match", matchArg,
		"active_bg=#cc0000", "active_fg=#ffffff", "inactive_bg=#880000", "inactive_fg=#cccccc")

	return func() {
		restoreTitle := oldTitle
		if restoreTitle == "" {
			restoreTitle = "bash"
		}
		kittenRun("set-tab-title", "--match", matchArg, restoreTitle)
		kittenRun("set-tab-color", "--match", matchArg,
			"active_bg=none", "active_fg=none", "inactive_bg=none", "inactive_fg=none")
	}
}

func tmuxSetupJailPane() func() {
	if os.Getenv("YOLO_NO_TMUX") == "1" {
		return nil
	}
	if os.Getenv("TMUX") == "" || !isattyStdin() {
		return nil
	}
	pane := os.Getenv("TMUX_PANE")
	jailDir := projectName()

	tmuxOpt := func(opt string) (string, bool) {
		out, err := tmuxCmd("show-option", "-pt", pane, opt)
		if err != nil {
			return "", false
		}
		// Presence only: `show-option -p` prints nothing at all when the pane has
		// no pane-local override, which is the case that must restore by UNSET.
		if strings.TrimSpace(string(out)) == "" {
			return "", false
		}
		// The value comes from -v, which prints the value ALONE and unquoted. The
		// -pt form prints `<name> "<value>"` with tmux's own quoting, and parsing
		// that kept the literal quote characters in the saved value. Trim only the
		// trailing newline: `pane-border-format` legitimately begins and ends with
		// a space (" 🔒 JAIL … "), and TrimSpace silently ate both.
		v, err := tmuxCmd("show-option", "-pvt", pane, opt)
		if err != nil {
			return "", false
		}
		return strings.TrimSuffix(strings.TrimSuffix(string(v), "\n"), "\r"), true
	}
	tmuxSet := func(opt, val string) {
		_, _ = tmuxCmd("set-option", "-pt", pane, opt, val)
	}

	borderOpts := []string{"pane-border-style", "pane-active-border-style", "pane-border-status", "pane-border-format"}
	// What this launch is about to write. Held as a map so the capture below can
	// recognise its OWN marks and decline to save them — see jailMark.
	jailValues := map[string]string{
		"pane-border-style":        "fg=red,bold",
		"pane-active-border-style": "fg=red,bold",
		"pane-border-status":       "bottom",
		"pane-border-format":       " 🔒 JAIL " + jailDir + " ",
	}
	old := map[string]*string{}
	for _, opt := range borderOpts {
		v, ok := tmuxOpt(opt)
		// A LATCH BREAKER, not an optimisation. If the value already there is one
		// yolo writes, a previous launch's restore did not run — killed by a signal,
		// or discarded by the argv bug below. Saving it would make this launch
		// "restore" the jail border permanently, and every launch after it would do
		// the same, so a single missed restore used to pin the border forever. A mark
		// we recognise as ours is therefore treated as absent, which lets the next
		// clean exit put the pane back even though the pane it found was dirty.
		//
		// It is keyed on the option's own jail value, not on a substring, so a user
		// whose real `pane-border-format` happens to mention a lock glyph keeps it.
		if ok && v == jailValues[opt] {
			ok = false
		}
		if ok {
			vv := v
			old[opt] = &vv
		} else {
			old[opt] = nil
		}
	}
	// jailWindowName is written below and recognised here, for the same reason the
	// border values are: a pane found already named JAIL is a previous launch that
	// did not clean up, and saving that name would rename the window to JAIL on
	// every exit forever. Treated as absent, so the restore leaves the name alone
	// and the next clean run has nothing to re-pin.
	//
	// KNOWN LIMIT, stated because the border half does better: an already-latched
	// window keeps the name JAIL. The border heals completely (it has an "unset"
	// spelling that falls back to the window/global value), but a name has no such
	// fallback and the original is unrecoverable — we cannot know what the window
	// was called before some earlier run renamed it. Turning `automatic-rename`
	// back on would re-derive one, and is deliberately NOT done: it would override
	// a user who turned it off on purpose, to fix a stale string.
	const jailWindowName = "JAIL"
	oldWindow := ""
	if out, err := tmuxCmd("display-message", "-p", "#{window_name}"); err == nil {
		if n := strings.TrimSpace(string(out)); n != jailWindowName {
			oldWindow = n
		}
	}
	oldAutoRename := ""
	if out, err := tmuxCmd("show-window-option", "-v", "automatic-rename"); err == nil {
		oldAutoRename = strings.TrimSpace(string(out))
	}

	for _, opt := range borderOpts {
		tmuxSet(opt, jailValues[opt])
	}
	_, _ = tmuxCmd("set-window-option", "automatic-rename", "off")
	_, _ = tmuxCmd("rename-window", jailWindowName)

	// Each command is built as its own ARGV SLICE and flattened without ever being
	// re-split. It used to be assembled as a string and split on whitespace, which
	// tore any value containing a space into separate arguments: a saved
	// `pane-border-format` of " 🔒 JAIL yolo-jail " became seven of them and tmux
	// answered `command set-option: too many arguments (need at most 2)`.
	//
	// That was catastrophic rather than partial, and it LATCHED. tmux parses a
	// ";"-separated list before executing any of it, so one bad command discards the
	// whole restore — and the next launch then captures the JAIL values as `old`,
	// which contain spaces, so every restore afterwards failed the same way and the
	// border never came back. Measured on tmux 3.7b.
	return func() {
		var cmds [][]string
		for _, opt := range borderOpts {
			if v := old[opt]; v != nil {
				cmds = append(cmds, []string{"set-option", "-pt", pane, opt, *v})
			} else {
				cmds = append(cmds, []string{"set-option", "-put", pane, opt})
			}
		}
		// A window name may contain spaces for the same reason, so it is one
		// argument too.
		if oldWindow != "" {
			cmds = append(cmds, []string{"rename-window", oldWindow})
		}
		if oldAutoRename == "on" {
			cmds = append(cmds, []string{"set-window-option", "automatic-rename", "on"})
		}
		if len(cmds) == 0 {
			return
		}
		var full []string
		for i, cmd := range cmds {
			if i > 0 {
				full = append(full, ";")
			}
			full = append(full, cmd...)
		}
		_, _ = tmuxCmd(full...)
	}
}
