package check

import (
	"bufio"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// cleanupTrackingFn removes a container's tracking file.
func cleanupTrackingFn(name string) {
	runtime.CleanupContainerTracking(name)
}

// expandUserPath expands a leading "~"/"~/…"
// against $HOME (or the passwd home). A bare "~user" form is left untouched.
func expandUserPath(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i == 1 {
		home := strings.TrimRight(userHome(), "/")
		res := home + p[i:]
		if res == "" {
			return "/"
		}
		return res
	}
	return p
}

func userHome() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		if h == "" {
			return "/"
		}
		return h
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "/"
}

func isExecutableFile(p string) bool {
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return isExecutable(p)
}

// readFileBytes reads a file (used by CDI-spec matching). Wrapper for testing.
func readFileBytes(p string) ([]byte, error) { return os.ReadFile(p) }

// loadConfigLoose returns the user + workspace configs merged, non-strict.
// Any error → nil (the caller substitutes {}).
func loadConfigLoose(workspace string) *jsonx.OrderedMap {
	cfg, err := config.LoadConfig(workspace, false, func(string) {})
	if err != nil {
		return nil
	}
	return cfg
}

// orphanCleanupPrompt runs the y/N prompt in the Running Jails block. Returns true
// iff the user answered y/yes.
//
// THE PROMPT WAS UNREACHABLE FOR ITS WHOLE LIFE, and the shape of that is worth
// keeping: nothing in internal/cli assigned `Options.Stdin`, so the nil branch below
// ran on every real invocation — the question printed, the answer was always "N", and
// the report then told you to run the command it had just declined to run for you.
// A reader checking the feature found a prompt, a reader checking the wiring found
// nothing to fix, and the two never met. `checkOptions` assigns Stdin now
// (TestCheckOptionsWiring pins it), which is the whole fix for the interactive case.
//
// The non-interactive case needed the OTHER half, or assigning Stdin would trade one
// wrong behaviour for a worse one: reading a piped stdin means `yolo check < script`
// in CI could answer "y" to a destructive prompt nobody saw. So the question is asked
// only where canAskAboutOrphans says it can be seen, and everywhere else the finding's
// note carries the command instead (nonTTYOrphansNote) — the same shape run's own
// reclaim offer uses (run/offer.go: "No prompt, no deletion, one line. Never an
// implicit yes.").
func (o *Options) orphanCleanupPrompt(r *reporter, n int) bool {
	if !o.canAskAboutOrphans() {
		return false
	}
	prompt := "  " + r.style(pluralOrphansPrompt(n), ansiYellow) + " "
	r.line(prompt)
	scanner := bufio.NewScanner(o.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// canAskAboutOrphans reports whether the orphan question can be seen and answered: a
// terminal on both ends, and a human report to print it in. `--format json` discards that
// report (outfmt.Sink), so a JSON run at a terminal would block on an answer to a question
// nobody was shown.
func (o *Options) canAskAboutOrphans() bool {
	return o.Stdin != nil && o.IsTTYStdout() && !outfmt.IsJSON(o.Format)
}

func pluralOrphansPrompt(n int) string {
	return "Stop " + itoa(n) + " orphaned jail(s)? [y/N]"
}

// orphanRemoval is the runtime command that removes the named jails: the one a terminal's yes
// runs for each orphan, and the one nonTTYOrphansNote prints for all of them, so the two cannot
// name different removals. Apple Container takes the --force spelling its launch path uses
// (run's removeStaleContainer); podman, -f.
func orphanRemoval(rt string, names ...string) []string {
	force := "-f"
	if rt == "container" {
		force = "--force"
	}
	return append([]string{rt, "rm", force}, names...)
}

// nonTTYOrphansNote is the orphan finding's note where no question can be asked: what is true,
// and the command that removes them, then the re-check. Never a prompt, so nothing can read an
// implicit yes out of redirected input.
//
// It used to send the reader to `yolo prune --apply`, which removes only STOPPED containers
// while these are running, so following it changed nothing. The command is the runtime's own
// removal, the one the terminal's yes runs; the yes also records, for each session the
// removal ends, why its jail stopped (run.RecordJailStop), which a removal from outside yolo
// cannot, so those sessions report a stop from outside yolo.
func nonTTYOrphansNote(rt string, names []string) string {
	return "These containers are stuck or have lost their workspace, and this check removes them\n" +
		"only when a terminal answers its question:\n" +
		"fix:  " + shquote.Join(orphanRemoval(rt, names...)) + "\n" +
		recheck
}

// pyStrOf renders a human string for a non-string cmd[0] element (rare/never
// for real config). Booleans → True/False, numbers → decimal, else JSON.
func pyStrOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "True"
		}
		return "False"
	case float64:
		return jsonx.FormatFloatRepr(t)
	default:
		if lit, ok := jsonx.AsIntLiteral(v); ok {
			return lit
		}
		s, _ := jsonx.DumpsCompact(v)
		return s
	}
}
