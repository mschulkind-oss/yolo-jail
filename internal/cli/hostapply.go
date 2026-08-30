package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// hostApply is `yolo host apply` — the ergonomic spelling of `yolo apply --at host`.
// Both remain; this one also owns --shell-init, which has no counterpart at any other
// notch because no other notch has a user's shell.
func hostApply(args []string, out, errw io.Writer, color bool, stdin io.Reader) int {
	assert, dryRun, shellInit := false, false, false
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, hostUsage)
			return 0
		case a == "--assert":
			assert = true
		case a == "--dry-run":
			dryRun = true
		case a == "--shell-init":
			shellInit = true
		default:
			fmt.Fprintf(errw, "yolo host apply: unexpected argument %q\n\n%s\n", a, hostUsage)
			return 2
		}
	}
	write := assert && !dryRun
	rc := applyHost(out, errw, color, write, stdin)
	if shellInit {
		if src := runShellInit(richtext.Printer{W: out, Color: color}, errw, write); src != 0 {
			rc = src
		}
	}
	return rc
}

// applyHostWrappers is the wrapper-generation stage of an apply.
//
// It runs at BOTH spellings of the host apply because it lives inside applyHost, not
// inside `yolo host apply` — the two are one operation and only differ in how they are
// typed (OQ-7).
//
// Reporting follows §5.5's ruling: apply announces its OWN ACTION, and only that. It
// prints the PATH line when it created or changed the directory, and stays silent
// otherwise. It never inspects PATH, because the PATH apply can see is a fact about the
// shell that happened to invoke it rather than about the user's rc file — an observation
// that is wrong in both directions (it nags after an rc edit made in another shell, and
// says nothing after a one-off `export`). `yolo check` carries that observation instead,
// where it is both decidable and actionable.
func applyHostWrappers(pr richtext.Printer, errw io.Writer, home string, packs []*packload.Pack, write bool) int {
	dir := paths.WrapDirUnder(home)
	enabled := config.HostWrappersEnabled()

	if !enabled {
		// Not opted in means NO directory and no messages at all — that is what keeps
		// any of this from being a nag. The one exception is cleaning up after the key
		// is turned back OFF: leaving live wrappers on a user's PATH after they said no
		// would be the worst of both.
		plan, err := hostwrap.PlanFor(dir, nil)
		if err != nil || !plan.Changed() {
			return 0
		}
		if !write {
			pr.Printf("  [cyan]%-20s[/cyan] would remove %d wrapper(s)  [dim]%s[/dim]",
				"host_wrappers", len(plan.Removed), dir)
			return 0
		}
		if _, err := hostwrap.Clear(dir); err != nil {
			fmt.Fprintf(errw, "yolo host apply: clearing wrappers: %v\n", err)
			return 1
		}
		pr.Printf("  [cyan]%-20s[/cyan] removed %d wrapper(s)  [dim]%s[/dim]",
			"host_wrappers", len(plan.Removed), dir)
		return 0
	}

	bins := hostwrap.Bins(packs)
	if !write {
		plan, err := hostwrap.PlanFor(dir, bins)
		if err != nil {
			fmt.Fprintf(errw, "yolo host apply: planning wrappers: %v\n", err)
			return 1
		}
		pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]",
			"host_wrappers", describeWrapperPlan(plan, false), dir)
		return 0
	}
	plan, err := hostwrap.Generate(dir, bins)
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply: generating wrappers: %v\n", err)
		return 1
	}
	pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]",
		"host_wrappers", describeWrapperPlan(plan, true), dir)
	if plan.Changed() {
		// The completion notice: "I just wrote these; here is what makes them take
		// effect." Conditioned on this apply's own action, never on an observation.
		pr.Printf("    [dim]add this to your shell rc to use them (or pass --shell-init):[/dim]")
		pr.Printf("    [bold]%s[/bold]", hostwrap.PathLine(dir))
	}
	return 0
}

func describeWrapperPlan(plan hostwrap.Plan, wrote bool) string {
	if !plan.Changed() {
		return fmt.Sprintf("%d wrapper(s), unchanged", len(plan.Wrappers))
	}
	var parts []string
	if n := len(plan.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d", n))
	}
	if n := len(plan.Rewritten); n > 0 {
		parts = append(parts, fmt.Sprintf("~%d", n))
	}
	if n := len(plan.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("-%d", n))
	}
	verb := "would write"
	if wrote {
		verb = "wrote"
	}
	return fmt.Sprintf("%s %d wrapper(s) (%s)", verb, len(plan.Wrappers), strings.Join(parts, " "))
}

// runShellInit appends the PATH line to the user's shell rc, on explicit request only.
//
// P3 says yolo does not silently edit shell rc files, and this does not weaken that: it
// runs only when the user typed --shell-init, it appends rather than rewriting, and it is
// idempotent — a file that already contains the line is left alone rather than
// accumulating a copy per apply.
func runShellInit(pr richtext.Printer, errw io.Writer, write bool) int {
	dir := paths.WrapDir()
	line := hostwrap.PathLine(dir)
	rc, err := shellRCPath()
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply --shell-init: %v\n", err)
		return 1
	}
	existing, err := os.ReadFile(rc)
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(errw, "yolo host apply --shell-init: reading %s: %v\n", rc, err)
		return 1
	}
	if strings.Contains(string(existing), dir) {
		pr.Printf("  [dim]%s already references the wrapper dir — left alone[/dim]", rc)
		return 0
	}
	if !write {
		pr.Printf("  [cyan]%-20s[/cyan] would append to %s", "--shell-init", rc)
		pr.Printf("    [bold]%s[/bold]", line)
		return 0
	}
	f, err := os.OpenFile(rc, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(errw, "yolo host apply --shell-init: opening %s: %v\n", rc, err)
		return 1
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n# yolo-jail host launch wrappers\n%s\n", line); err != nil {
		fmt.Fprintf(errw, "yolo host apply --shell-init: writing %s: %v\n", rc, err)
		return 1
	}
	pr.Printf("  [green]appended the PATH line to %s[/green] — open a new shell to pick it up", rc)
	return 0
}

// shellRCPath picks the rc file to append to from $SHELL, defaulting to ~/.bashrc.
//
// Guessing is acceptable HERE and nowhere else in this design: the user asked for the
// write, so a wrong guess is a visible file they can move a line out of, rather than the
// silent misreport that reading rc files to INFER state would produce.
func shellRCPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve your home: %w", err)
	}
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "fish":
		// fish cannot source a POSIX export line; say so rather than writing something
		// that will not work.
		return "", fmt.Errorf("--shell-init does not know fish syntax yet — add this to "+
			"your fish config by hand:\n  fish_add_path %s", paths.WrapDir())
	default:
		return filepath.Join(home, ".bashrc"), nil
	}
}

// setHostWrappers turns the opt-in on or off in the USER config, preserving comments.
//
// It is a targeted textual edit rather than a parse-and-reserialize because the user
// config is JSONC that a human wrote and yolo does not own: round-tripping it through a
// JSON encoder would silently delete every comment in the file.
func setHostWrappers(enabled bool) error {
	path := paths.UserConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path,
			[]byte(fmt.Sprintf("{\n  // Host launch wrappers — see `yolo config-ref`.\n  \"host_wrappers\": %v\n}\n", enabled)),
			0o644)
	}
	updated, ok := setJSONCBool(string(data), "host_wrappers", enabled)
	if !ok {
		return fmt.Errorf("could not find a place to set host_wrappers in %s — "+
			"add `\"host_wrappers\": %v` to it by hand", path, enabled)
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// setJSONCBool replaces `"key": <bool>` if present, else inserts it after the first `{`.
// Reports false when it cannot find an insertion point.
func setJSONCBool(text, key string, value bool) (string, bool) {
	needle := `"` + key + `"`
	if i := strings.Index(text, needle); i >= 0 {
		rest := text[i+len(needle):]
		colon := strings.Index(rest, ":")
		if colon >= 0 {
			after := rest[colon+1:]
			trimmed := strings.TrimLeft(after, " \t")
			pad := after[:len(after)-len(trimmed)]
			for _, lit := range []string{"true", "false"} {
				if strings.HasPrefix(trimmed, lit) {
					return text[:i+len(needle)] + rest[:colon+1] + pad +
						fmt.Sprintf("%v", value) + trimmed[len(lit):], true
				}
			}
		}
	}
	brace := strings.Index(text, "{")
	if brace < 0 {
		return text, false
	}
	insert := fmt.Sprintf("\n  \"%s\": %v,", key, value)
	return text[:brace+1] + insert + text[brace+1:], true
}
