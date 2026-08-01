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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/depcheck"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

	reqs := configuredDepRequirements()
	if len(reqs) == 0 {
		fmt.Fprintln(out, "no host-dep hints declared by the configured packs — nothing to check.")
		return 0
	}
	results := depcheck.Check(reqs)

	pr := richtext.Printer{W: out, Color: color}
	missing := depcheck.Missing(results)
	for _, r := range results {
		switch {
		case r.Present:
			pr.Printf("[green]✓[/green] %-16s %s", r.Bin, r.Path)
		case r.Remedy != "":
			pr.Printf("[red]✗[/red] %-16s MISSING → %s", r.Bin, r.Remedy)
		default:
			pr.Printf("[yellow]?[/yellow] %-16s MISSING, no install hint for this host", r.Bin)
		}
	}
	if len(missing) == 0 {
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
// adapted to depcheck.Requirement. Embedded/local/fetched all contribute — a dep is a
// dep regardless of origin. Best-effort: a pack that fails to load is skipped (the run
// path reports load failures loudly; this verb is a probe, not a gate on loading).
func configuredDepRequirements() []depcheck.Requirement {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil
	}
	var reqs []depcheck.Requirement
	for _, e := range entries {
		p := packForCheckDeps(e)
		if p == nil {
			continue
		}
		for _, d := range p.Decl.DepRequirements() {
			reqs = append(reqs, depcheck.Requirement{Bin: d.Bin, Hints: d.Hints})
		}
	}
	return reqs
}

// packForCheckDeps loads one configured pack's declaration for dep inspection, or nil.
func packForCheckDeps(e config.PackEntry) *packload.Pack {
	if e.Embedded() {
		for _, p := range packload.Embedded() {
			if p.Name == e.Name {
				return p
			}
		}
		return nil
	}
	if !e.IsLocal() {
		return nil // a git pack must be `pack install`ed first; probe what is resolvable
	}
	// A local (file://) pack: read its manifest straight from the source dir. Dep
	// requirements are declared in pack.json, so no staging is needed to inspect them.
	root := strings.TrimPrefix(e.Source, "file://")
	p, _ := packload.LoadDir(root, e.Name, e.MayGrantHostFiles())
	return p
}

const checkDepsUsage = `yolo check-deps — probe the host for binaries the configured packs need

Below the jail notch yolo bakes no image, so a pack's tools become a question about the
host. This probes for each declared binary and, for the missing ones, prints the install
command for your package manager and writes a bundle manifest (~/.config/yolo/Brewfile
and kin) you can run in one step.

  yolo check-deps               probe + write the manifest for missing deps
  yolo check-deps --no-manifest probe only, write nothing

It never installs anything — it detects and hands off. Exit is non-zero when a declared
dep is missing.`
