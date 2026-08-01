// Package depcheck is the shared host-dependency checker (env-manager plan Phase 6,
// OQ-8). Below the jail notch yolo bakes no image, so a pack's `packages`/`program`
// dependency becomes a question about the host — is the binary present, and if not what
// installs it. The design's rule: ONE checker, used by `check`, by `apply`, and by a
// project's own doctor, over ONE declared list (the pack's install_hints) — so nobody
// re-implements "is psql present." OQ-8 chose a declared schema with a reference checker
// over it (this package) rather than a Go-package-only boundary, so a third-party doctor
// can read the same hints and probe with its own code.
//
// This package is deliberately dependency-light — it takes plain requirements (bin +
// per-manager hints) and returns plain results, so both the CLI and a standalone
// `yolo check-deps` call it without dragging config/entrypoint in.
package depcheck

import (
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// Requirement is one binary a host must provide, with the per-manager package names
// that install it. Mirrors packdecl.DepRequirement but kept local so this package has
// no packdecl dependency (the caller adapts).
type Requirement struct {
	Bin   string
	Hints map[string]string // manager ("brew"|"apt"|"dnf"|"pacman"|"nix") -> package
}

// Result is the probe outcome for one Requirement.
type Result struct {
	Bin     string
	Present bool
	Path    string // resolved path when present
	// Remedy is the install command for the detected host package manager, or "" when
	// no hint covers it (reported as unprobeable-remedy, never as satisfied).
	Remedy  string
	Manager string // the detected manager the remedy is for
}

// LookPath is the probe seam — overridable in tests so a check does not depend on the
// host's real PATH.
var LookPath = exec.LookPath

// DetectManager returns the host package manager to prefer for remedies. Overridable in
// tests. Order: on macOS prefer brew; on Linux probe apt/dnf/pacman in turn; nix last as
// the always-available fallback (it is the jail's manager too).
var DetectManager = detectManager

func detectManager() string {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath("brew"); err == nil {
			return "brew"
		}
	}
	for _, m := range []string{"apt", "dnf", "pacman", "brew"} {
		if _, err := exec.LookPath(m); err == nil {
			return m
		}
	}
	return "nix"
}

// Check probes every requirement against the host and returns the results in Bin order.
// It never installs anything — it reports (BACKLOG's "detect vs. apply" split); the
// caller decides whether to offer to run the remedies.
func Check(reqs []Requirement) []Result {
	mgr := DetectManager()
	var out []Result
	for _, r := range reqs {
		res := Result{Bin: r.Bin, Manager: mgr}
		if p, err := LookPath(r.Bin); err == nil {
			res.Present, res.Path = true, p
		} else if pkg, ok := r.Hints[mgr]; ok {
			res.Remedy = installCmd(mgr, pkg)
		}
		out = append(out, res)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bin < out[j].Bin })
	return out
}

// installCmd builds the per-manager install command for a package.
func installCmd(mgr, pkg string) string {
	switch mgr {
	case "brew":
		return "brew install " + pkg
	case "apt":
		return "sudo apt install -y " + pkg
	case "dnf":
		return "sudo dnf install -y " + pkg
	case "pacman":
		return "sudo pacman -S --noconfirm " + pkg
	case "nix":
		return "nix profile install nixpkgs#" + pkg
	default:
		return ""
	}
}

// Missing returns the results that are absent AND have a remedy — the actionable set.
func Missing(results []Result) []Result {
	var out []Result
	for _, r := range results {
		if !r.Present {
			out = append(out, r)
		}
	}
	return out
}

// Manifest renders the missing remedies as the package manager's own bundle file — a
// Brewfile for brew, a plain `pkg pkg pkg` install line for the others — so the user
// tunes the host up in one step rather than running N lines. Returns ("", "") when
// nothing is missing or nothing has a remedy.
func Manifest(results []Result) (filename, body string) {
	mgr := ""
	var pkgs []string
	for _, r := range results {
		if r.Present || r.Remedy == "" {
			continue
		}
		mgr = r.Manager
		// The package name is the last token of the remedy for brew/nix; for apt/dnf/
		// pacman it is also the trailing token. Recover it rather than re-plumbing.
		fields := strings.Fields(r.Remedy)
		pkgs = append(pkgs, fields[len(fields)-1])
	}
	if len(pkgs) == 0 {
		return "", ""
	}
	sort.Strings(pkgs)
	switch mgr {
	case "brew":
		var b strings.Builder
		for _, p := range pkgs {
			b.WriteString("brew \"" + p + "\"\n")
		}
		return "Brewfile", b.String()
	default:
		return mgr + "-packages.txt", strings.Join(pkgs, "\n") + "\n"
	}
}
