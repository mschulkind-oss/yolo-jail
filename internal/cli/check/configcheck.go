package check

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	userConfig, workspaceConfig, src, parseFailed := opts.sectionConfigFilesWithStrictness(r, opts.Workspace, true)
	if parseFailed {
		r.setScopeNotReached([]string{"merged config semantics", "selected-pack declarations and cross-pack checks"})
		r.sectionHeader("Scope")
		r.setScopeNotChecked(configCheckScopeEntries(opts.inJail()))
		r.renderScopeNotChecked()
		r.blank()
		return finishConfigCheck(opts, r)
	}
	merged := mergeScopes(userConfig, workspaceConfig, src, opts.Workspace)
	selection, _, packErr := config.UserScopeSelectedPackResolution()
	var resolver config.LoopholeResolver
	if packErr == nil {
		// Keep validating declarations that were readable. An unavailable sibling is reported
		// below as partial coverage; it must not turn known module settings into unknown names.
		resolver = config.SelectedPackLoopholeResolver(selection.Packs())
	}
	validationOptions := config.ValidationOptions{SkipLocalCloneGit: true, SkipHostPathFacts: opts.inJail()}
	errors, warnings := config.ValidateConfigWithOptions(merged, opts.Workspace, resolver, validationOptions)
	if source := src.orNone().merged; source != nil {
		errors = source.Annotate(errors)
		warnings = source.Annotate(warnings)
	}
	launch := &config.CapabilityLaunch{
		Packs: func() ([]*packload.Pack, bool) { return selection.Packs(), selection.Complete() },
		ProfileSets: func(packs []*packload.Pack) map[string][]string {
			return packload.ProfileSets(config.ConfigProfileSets(merged, packs))
		},
	}
	capErrors, capWarnings := capabilityGap(merged, opts.Getenv(allowUnmetCapabilitiesEnv), launch)
	errors = append(errors, capErrors...)
	warnings = append(warnings, capWarnings...)
	errors = append(errors, profileDeclarationGap(merged, selection.Packs(), selection.Complete(),
		func(selected []*packload.Pack) (map[string]packload.UserProfile, error) {
			return config.LoadSelectableProfiles(nil, merged, selected)
		}, func() []*packload.Pack { return selection.Packs() })...)
	collisionFindings := selectedPackCollisionFindings(selection.Packs())
	r.sectionHeader("Merged Configuration")
	for _, msg := range warnings {
		r.warn(termsafe.VisibleLines(msg), configNote(msg, opts.Workspace, configRecheck(r.mode)))
	}
	for _, msg := range errors {
		r.fail(termsafe.VisibleLines(msg), configNote(msg, opts.Workspace, configRecheck(r.mode)))
	}
	if len(errors) == 0 && len(collisionFindings) == 0 {
		r.ok("Merged config is semantically valid")
	}
	r.sectionHeader("Selected Packs")
	for _, finding := range collisionFindings {
		r.fail(finding.message, finding.note)
	}
	configPackCoverage(r, selection, packErr)

	r.sectionHeader("Scope")
	r.setScopeNotChecked(configCheckScopeEntries(opts.inJail()))
	r.renderScopeNotChecked()
	r.blank()
	return finishConfigCheck(opts, r)
}

func finishConfigCheck(opts Options, r *reporter) int {
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
			errors.Is(unresolved.Err, packsrc.ErrGitUnavailable) ||
			errors.Is(unresolved.Err, packsrc.ErrDefaultRefUnavailable) {
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

func configCheckScopeEntries(inJail bool) []ScopeEntry {
	entries := []ScopeEntry{
		{Area: "Runtime availability and executable resolution", Reason: "machine readiness is outside config validation", Verb: "yolo check"},
		{Area: "Nix", Reason: "machine readiness is outside config validation", Verb: "yolo check"},
		{Area: "Image, services, and loophole liveness", Reason: "machine readiness is outside config validation", Verb: "yolo check"},
		{Area: "Entrypoint dry-run", Reason: "it renders more than the selected declarations", Verb: "yolo check"},
		{Area: "Config-change approval", Reason: "approval is separate from validation", Verb: "yolo check --accept-config-changes"},
		{Area: "Local-clone repository status", Reason: "Git outside an existing fetched-pack store is not run by config validation", Verb: "yolo check"},
		{Area: "Disk inventory and reclaim", Reason: "not part of config validation", Verb: "yolo stores / yolo prune"},
	}
	if inJail {
		entries = append(entries, ScopeEntry{Area: "Host paths and mounts", Reason: "this is a generated snapshot; host facts are unverified", Verb: "run `yolo config check` on the host"})
	}
	return entries
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
