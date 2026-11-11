package check

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// ConfigCheck validates the readable user and workspace configuration without entering the
// machine-readiness Check pipeline. It intentionally does not call Check: that pipeline ensures
// storage and probes runtimes, Nix, binaries, services and the image.
func ConfigCheck(opts Options) int {
	if opts.Workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			opts.Workspace = wd
		} else {
			opts.Workspace = "."
		}
	}
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.PathExists == nil {
		opts.PathExists = func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		}
	}
	if opts.Writable == nil {
		opts.Writable = writableReal
	}
	isTTYStdout := opts.IsTTYStdout
	if isTTYStdout == nil {
		isTTYStdout = func() bool { return tty.IsTerminalFile(os.Stdout) }
	}
	color := tty.Color(opts.Getenv, opts.Color, isTTYStdout())
	r := newReporter(outfmt.Sink(opts.Stdout, opts.Format), color && !outfmt.IsJSON(opts.Format))
	r.mode = "config"
	r.scope = "user+workspace"
	r.sectionHeader("Config Check")
	r.blank()

	// Reuse the broad check's source-reading and provenance path. This only reads config and
	// emits disclosure; machine probes remain below the separate Check entrypoint.
	userConfig, workspaceConfig, src, parseFailed := opts.sectionConfigFilesWithStrictness(r, opts.Workspace, false)
	merged := mergeScopes(userConfig, workspaceConfig, src, opts.Workspace)
	selection, packProblems, packErr := config.UserScopeSelectedPackResolution()
	var resolver config.LoopholeResolver
	if packErr == nil && len(packProblems) == 0 && selection.Complete() {
		resolver = config.SelectedPackLoopholeResolver(selection.Packs())
	}
	errors, warnings := config.ValidateConfig(merged, opts.Workspace, resolver)
	if source := src.orNone().merged; source != nil {
		errors = source.Annotate(errors)
		warnings = source.Annotate(warnings)
	}
	for _, msg := range warnings {
		r.warn(termsafe.VisibleLines(msg), configNote(msg, opts.Workspace))
	}
	for _, msg := range errors {
		r.fail(termsafe.VisibleLines(msg), configNote(msg, opts.Workspace))
	}
	if len(errors) == 0 && !parseFailed {
		r.ok("Merged config is semantically valid")
	}
	configPackCoverage(r, selection, packErr)

	r.sectionHeader("Scope")
	r.dim("Checks config parsing and semantics for user and workspace config")
	r.dim("Does not check runtime, Nix, programs, services, image readiness, or disk usage")
	r.blank()
	r.sectionHeader("Summary")
	parts := []string{r.styledCount(r.failed, "failed", ansiRed)}
	if r.warned > 0 {
		parts = append(parts, r.styledCount(r.warned, "warnings", ansiYellow))
	}
	if r.skipped > 0 {
		parts = append(parts, r.styledCount(r.skipped, "pack checks skipped", ansiDimBadge))
	}
	r.line("  " + strings.Join(parts, ", "))
	r.blank()
	rc := 0
	if r.failed > 0 {
		rc = 1
	}
	return finish(opts.Stdout, opts.Stderr, opts.Format, r, rc)
}

// configPackCoverage reports only gaps that prevent reading selected fetched-pack declarations.
// A readable malformed declaration remains a FAIL; it is never downgraded to skipped coverage.
func configPackCoverage(r *reporter, selection config.PackSelection, loadErr error) {
	if loadErr != nil {
		return // ValidateConfig reports malformed/unreadable user configuration.
	}
	for _, unresolved := range selection.Unresolved {
		name := unresolved.Entry.Name
		if errors.Is(unresolved.Err, packsrc.ErrNotFetched) ||
			errors.Is(unresolved.Err, packsrc.ErrNotCheckedOut) ||
			errors.Is(unresolved.Err, packsrc.ErrRevisionMissing) ||
			errors.Is(unresolved.Err, packsrc.ErrGitUnavailable) {
			note := unresolved.Err.Error()
			if errors.Is(unresolved.Err, packsrc.ErrGitUnavailable) {
				note += "; install or repair Git, then rerun `yolo config check`"
			} else {
				note += "; run `yolo pack install` to fetch or check out the selected pack, then rerun `yolo config check`"
			}
			r.skip("Selected pack "+name+" declaration checks skipped", note)
			continue
		}
		r.fail("Selected pack "+name+" declarations could not be validated", unresolved.Err.Error())
	}
	if selection.ClosureErr != nil {
		r.fail("Selected pack closure could not be validated", selection.ClosureErr.Error())
	}
}

// ConfigCheckHelp is the public help text for the config-only command.
func ConfigCheckHelp(out io.Writer) {
	fmt.Fprintln(out, `Usage: yolo config check [--json | --format json]

Validate the readable user and workspace configuration. This is not a machine-readiness check;
use 'yolo check' for runtime, Nix, service, and image readiness.

Flags:
  --json                   Render the same findings as JSON.
  --format json            Render the same findings as JSON.
  --help, -h               Show this help.`)
}
