package cli

// checkdeps.go is `yolo check-deps` — the standalone entry point to the shared
// dep-checker (env-manager plan Phase 6). It probes the host for every binary the
// configured packs declare install_hints for, reports present/missing, and — because a
// wall of `→ install X` lines is one step short of useful — writes the package
// manager's own manifest (a Brewfile and kin) so the user tunes the host up in one step.
//
// It NEVER installs anything (BACKLOG's detect-vs-apply split): it detects and hands off
// with the command. The offer-to-run (behind a batched, sudo-shown-through confirm,
// OQ-9) belongs to `apply` at a lower notch — this verb is the probe half, usable by a
// project's own doctor over the same declared hints.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func runCheckDeps(args []string) int {
	return checkDepsMain(args[1:], os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

func checkDepsMain(args []string, out, errw io.Writer, color bool) int {
	writeManifest := true
	for _, a := range args {
		switch {
		case isHelpToken(a):
			io.WriteString(out, checkDepsUsage+"\n")
			return 0
		case a == "--no-manifest":
			writeManifest = false
		default:
			fmt.Fprintf(errw, "yolo check-deps: unexpected argument %q\n\n%s\n", a, checkDepsUsage)
			return 2
		}
	}

	reqs, unresolved := configuredDepRequirements()
	pr := richtext.Printer{W: out, Color: color}
	// NAMED, NEVER SKIPPED: a pack this probe could not resolve declares deps nobody looked
	// at, so "nothing missing" would be a claim about binaries it never checked.
	for _, u := range unresolved {
		pr.Printf("[red]✗[/red] pack %s could not be resolved, so its deps were not probed: %s",
			u.Name, u.Reason)
	}
	if len(reqs) == 0 {
		fmt.Fprintln(out, "no host-dep hints declared by the resolved packs — nothing to check.")
		if len(unresolved) > 0 {
			return 1
		}
		return 0
	}
	results := depcheck.Check(reqs)

	missing := depcheck.Missing(results)
	for _, r := range results {
		switch {
		case r.Present:
			pr.Printf("[green]✓[/green] %-16s %s", r.Bin, r.Path)
		case r.Remedy != "":
			pr.Printf("[red]✗[/red] %-16s MISSING → %s", r.Bin, r.Remedy)
			// The package-manager alternative for a dep whose primary remedy is the tool's
			// own installer. Shown because a user who would rather go through their package
			// manager should not have to read pack.json to find the token — but shown SECOND,
			// since the first-party installer is the one that stays current.
			if r.Fallback != "" {
				pr.Printf("  [dim]or via %s: %s[/dim]", r.Manager, r.Fallback)
			}
		default:
			pr.Printf("[yellow]?[/yellow] %-16s MISSING, no install hint for this host", r.Bin)
		}
	}
	if len(missing) == 0 {
		if len(unresolved) > 0 {
			return 1 // an unprobed pack is not a clean bill of health
		}
		return 0
	}

	if writeManifest {
		if name, body := depcheck.Manifest(results); name != "" {
			p := filepath.Join(depManifestDir(), name)
			if err := os.MkdirAll(depManifestDir(), 0o755); err == nil &&
				os.WriteFile(p, []byte(body), 0o644) == nil {
				pr.Printf("")
				pr.Printf("[dim]wrote %s — install with the command for your manager[/dim]", p)
			}
		}
	}
	// Missing deps are a non-zero exit so a CI or a caller can gate on it.
	return 1
}

// depManifestDir is the fixed, user-scoped home for the generated dep manifest
// (~/.config/yolo). Env-manager plan Phase 6 wants this to become a composed surface
// regenerated every apply; this standalone verb writes it directly for now.
func depManifestDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "yolo")
}

// configuredDepRequirements collects DepRequirements across every configured pack,
// adapted to depcheck.Requirement, plus every configured pack it could not resolve.
// Embedded/local/fetched all contribute — a dep is a dep regardless of origin, and a git
// pack resolves from the store the way a launch resolves it (resolveConfiguredPack). A
// pack that does not resolve is RETURNED rather than dropped: this verb is a probe, not a
// gate on loading, but a probe that silently skipped a pack would report "nothing missing"
// about deps it never looked at.
//
// The per-pack adaptation lives in packDepRequirements (applyhostdeps.go) because
// `yolo host apply` needs the same projection one pack at a time; keeping one adapter is what
// stops the two commands from disagreeing about what counts as a requirement.
func configuredDepRequirements() ([]depcheck.Requirement, []unresolvedPack) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, nil
	}
	var reqs []depcheck.Requirement
	var unresolved []unresolvedPack
	for _, e := range entries {
		p, err := resolveConfiguredPack(e)
		if err != nil {
			unresolved = append(unresolved, newUnresolvedPack(e.Name, err))
			continue
		}
		reqs = append(reqs, packDepRequirements(p)...)
	}
	return reqs, unresolved
}

// unresolvedPack is one configured pack that could not be resolved, and the resolver's own
// words for why — which name the remedy (`yolo pack install` for a git pack the store does not
// have, `yolo pack update` for a ref its mirror lacks, the path for a missing local dir).
type unresolvedPack struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// NeedsInstall is whether the fix is `yolo pack install`: a git pack whose address parsed
	// but whose mirror, ref or tree is not in the store. Carried as a fact decided where the
	// failure happened, never recovered from the reason's wording.
	NeedsInstall bool `json:"needs_install"`
}

// newUnresolvedPack records a resolution failure from resolveConfiguredPack. The resolver's
// "packs: <name>: " prefix is dropped from the reason, because every report prints the name
// beside it.
func newUnresolvedPack(name string, err error) unresolvedPack {
	var miss storeMissError
	return unresolvedPack{Name: name, Reason: strings.TrimPrefix(err.Error(), "packs: "+name+": "),
		NeedsInstall: errors.As(err, &miss)}
}

// storeMissError marks a git pack the pack store cannot supply — never fetched, a ref the
// mirror lacks, a subpath absent at the resolved commit. `yolo pack install` re-fetches every
// configured git pack, so it is the remedy for exactly this class and no other.
type storeMissError struct{ err error }

func (e storeMissError) Error() string { return e.err.Error() }
func (e storeMissError) Unwrap() error { return e.err }

// unresolvedNames is the names alone, for the reports that list them.
func unresolvedNames(list []unresolvedPack) []string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		out = append(out, u.Name)
	}
	return out
}

// describeUnresolved is the one-line human form: every pack with its reason.
func describeUnresolved(list []unresolvedPack) string {
	parts := make([]string, 0, len(list))
	for _, u := range list {
		parts = append(parts, u.Name+" ("+u.Reason+")")
	}
	return strings.Join(parts, "; ")
}

// resolveConfiguredPack loads one configured pack's declaration from wherever a LAUNCH would
// find it, or says why it cannot.
//
// ONE RESOLUTION RULE, THE LAUNCH'S. An embedded pack comes from the binary; every other one
// goes through run.PackRoot — the function stagePacks calls — so a git pack resolves OFFLINE
// from the pack store (`yolo pack install` is what puts it there) and a local one from its path,
// with the same staged-tree fallback a nested launch relies on. This loader used to return
// nothing for every git pack without asking the store, so `yolo host apply` skipped a pack the
// user HAD installed and told them to install it.
//
// A FETCHED TREE IS CHECKED THE WAY A LAUNCH STAGES IT. The host notch reads a pack in place
// rather than staging it, which is harmless for a local pack (a tree the user pointed at
// themselves) and is not for a fetched one: packstage's NO-ESCAPE rule is what stops a third-
// party repo's `ln -s ~/.ssh/id_ed25519 skills/x/SKILL.md` from delivering a secret, and at this
// notch the content lands in the real home, where agents read it. So a fetched pack is staged
// into a throwaway directory first — packstage.Stage, the launch's own rule, with the entry's
// filters — and a refusal there makes the pack unresolvable, exactly as it would fail the launch.
//
// Manifest problems are DISCARDED here, as they always were: `yolo check` and `yolo pack lint`
// report them, and the host-notch guards (packload's containment checks) exist for this caller.
func resolveConfiguredPack(e config.PackEntry) (*packload.Pack, error) {
	if e.Embedded() {
		for _, p := range packload.Embedded() {
			if p.Name == e.Name {
				return p, nil
			}
		}
		return nil, fmt.Errorf("packs: %s: this build of yolo ships no pack by that name", e.Name)
	}
	root, err := run.PackRoot(e, nil)
	if err != nil {
		if _, perr := packsrc.Parse(e.Source); perr == nil && !e.IsLocal() {
			return nil, storeMissError{err}
		}
		return nil, err
	}
	if !e.IsLocal() {
		if err := checkFetchedTreeStages(e, root); err != nil {
			return nil, err
		}
	}
	p, probs := packload.LoadDir(root, e.Name)
	if p == nil {
		return nil, fmt.Errorf("packs: %s: %s", e.Name, strings.Join(probs, "; "))
	}
	return p, nil
}

// checkFetchedTreeStages runs the launch's staging rule over a fetched pack's tree into a
// throwaway directory, and returns its refusal (see resolveConfiguredPack for why).
func checkFetchedTreeStages(e config.PackEntry, root string) error {
	dest, err := os.MkdirTemp("", "yolo-host-pack-check-")
	if err != nil {
		return fmt.Errorf("packs: %s: %w", e.Name, err)
	}
	defer os.RemoveAll(dest)
	if _, err := packstage.Stage(packstage.Spec{
		Root: root, Dest: dest, Only: e.Only, Exclude: e.Exclude,
	}); err != nil {
		return fmt.Errorf("packs: %s: %w", e.Name, err)
	}
	return nil
}

// packForCheckDeps is resolveConfiguredPack for a caller that has nothing to say about a pack
// it cannot resolve — today only test helpers. Every production caller takes the error, because
// a pack skipped in silence is the half state `yolo host apply` refuses.
func packForCheckDeps(e config.PackEntry) *packload.Pack {
	p, _ := resolveConfiguredPack(e)
	return p
}

const checkDepsUsage = `yolo check-deps — probe the host for binaries the configured packs need

Below the jail notch yolo bakes no image, so a pack's tools become a question about the
host. This probes for each declared binary and, for the missing ones, prints the install
command for your package manager and writes a bundle manifest (~/.config/yolo/Brewfile
and kin) you can run in one step.

  yolo check-deps               probe + write the manifest for missing deps
  yolo check-deps --no-manifest probe only, write nothing

Packs resolve the way a launch resolves them: a git pack from the pack store
(` + "`yolo pack install`" + ` fetches it), a local one from its path. A configured pack that
cannot be resolved is named with the reason, and its deps are not probed.

It never installs anything — it detects and hands off. Exit is non-zero when a declared
dep is missing, or when a configured pack could not be resolved.

Examples:
  yolo check-deps                     # what is missing, and write the bundle manifest
  yolo check-deps --no-manifest       # just tell me, write nothing`
