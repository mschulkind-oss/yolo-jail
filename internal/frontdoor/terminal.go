// Package frontdoor renders the terminal-facing half of the `yolo` run: the
// tmux/kitty jail indicators (SetupJailIndicator) and the start-of-run banner
// (StartupBanner). The banner's platform naming is x86_64/aarch64
// (platform.machine()), NOT Go's amd64/arm64.
package frontdoor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

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

func isattyStdin() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
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
		out, err := exec.Command("tmux", "show-option", "-pt", pane, opt).Output()
		if err != nil {
			return "", false
		}
		s := strings.TrimSpace(string(out))
		if s == "" {
			return "", false
		}
		parts := strings.SplitN(s, " ", 2)
		if len(parts) > 1 {
			return parts[1], true
		}
		return "", true
	}
	tmuxSet := func(opt, val string) {
		c := exec.Command("tmux", "set-option", "-pt", pane, opt, val)
		c.Stdout, c.Stderr = nil, nil
		_ = c.Run()
	}

	borderOpts := []string{"pane-border-style", "pane-active-border-style", "pane-border-status", "pane-border-format"}
	old := map[string]*string{}
	for _, opt := range borderOpts {
		if v, ok := tmuxOpt(opt); ok {
			vv := v
			old[opt] = &vv
		} else {
			old[opt] = nil
		}
	}
	oldWindow := ""
	if out, err := exec.Command("tmux", "display-message", "-p", "#{window_name}").Output(); err == nil {
		oldWindow = strings.TrimSpace(string(out))
	}
	oldAutoRename := ""
	if out, err := exec.Command("tmux", "show-window-option", "-v", "automatic-rename").Output(); err == nil {
		oldAutoRename = strings.TrimSpace(string(out))
	}

	tmuxSet("pane-border-style", "fg=red,bold")
	tmuxSet("pane-active-border-style", "fg=red,bold")
	tmuxSet("pane-border-status", "bottom")
	tmuxSet("pane-border-format", " 🔒 JAIL "+jailDir+" ")
	runQuiet("tmux", "set-window-option", "automatic-rename", "off")
	runQuiet("tmux", "rename-window", "JAIL")

	return func() {
		var cmds []string
		for _, opt := range borderOpts {
			if v := old[opt]; v != nil {
				cmds = append(cmds, "set-option -pt "+pane+" "+opt+" "+*v)
			} else {
				cmds = append(cmds, "set-option -put "+pane+" "+opt)
			}
		}
		if oldWindow != "" {
			cmds = append(cmds, "rename-window "+oldWindow)
		}
		if oldAutoRename == "on" {
			cmds = append(cmds, "set-window-option automatic-rename on")
		}
		if len(cmds) == 0 {
			return
		}
		full := []string{}
		for i, cmd := range cmds {
			if i > 0 {
				full = append(full, ";")
			}
			full = append(full, strings.Fields(cmd)...)
		}
		runQuiet("tmux", full...)
	}
}

func runQuiet(name string, args ...string) {
	c := exec.Command(name, args...)
	c.Stdout, c.Stderr = nil, nil
	_ = c.Run()
}

// hostPlatform returns "<goos>/<machine>" matching Python's
// f"{sys.platform}/{platform.machine()}", using the running GOOS/GOARCH.
func hostPlatform() string {
	return runtime.GOOS + "/" + platformMachine(runtime.GOOS, runtime.GOARCH)
}

// platformMachine maps Go's GOARCH to Python's platform.machine() spelling for
// the given GOOS. It is a pure function of (goos, goarch) so every OS/arch combo
// is unit-testable, not just the one the tests happen to run on. The spelling is
// Python's, NOT Go's amd64/arm64: amd64→x86_64 everywhere; arm64→aarch64 ONLY on
// Linux — on macOS/Apple Silicon platform.machine() is "arm64" (audit 2026-07-18
// §C: the unconditional arm64→aarch64 map was wrong on macOS and a test locked
// the bug). Any other GOARCH passes through unchanged.
func platformMachine(goos, goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		if goos != "darwin" {
			return "aarch64" // Linux uname; macOS keeps arm64
		}
		return "arm64"
	default:
		return goarch
	}
}

// StartupBanner formats the start-of-run banner line(s) exactly as
// _print_startup_banner writes them to stderr. jailVersion is shown only when
// it differs from version. resParts, if non-empty, adds the resource-limits line.
func StartupBanner(version, runtime_, cname string, resParts []string, jailVersion string) string {
	var verPart string
	if jailVersion != "" && jailVersion != version {
		verPart = fmt.Sprintf("yolo-jail %s (attached to jail built at %s)", version, jailVersion)
	} else {
		verPart = "yolo-jail " + version
	}
	parts := []string{verPart, hostPlatform(), runtime_, cname}
	line := strings.Join(parts, " | ")
	if len(resParts) > 0 {
		line += "\nResource limits: " + strings.Join(resParts, ", ")
	}
	return line
}
