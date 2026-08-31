package run

import (
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// resolveRepoRoot locates the yolo-jail repo root for nix image builds. Returns
// (resolution, ok); ok=false is the exit(1) branch (with an actionable message
// printed to stderr). The resolution itself is the single shared method in
// internal/reporoot — identical inside and outside the jail, and identical in
// every directory — so run and check never drift. This wrapper adds only the
// run-side error banner (reporoot.Resolve is pure and never prints).
func resolveRepoRoot(getenv func(string) string, stderr io.Writer, color bool) (reporoot.Resolution, bool) {
	if res, ok := reporoot.Resolve(getenv); ok {
		return res, true
	}
	if stderr != nil {
		pr := printer{rt: richtext.Printer{W: stderr}}
		pr.print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
			"The yolo CLI needs the repo for nix image builds.\n\n" +
			"Fix: install so the flake bundle ships with the binary (`just install`), or\n" +
			"point yolo at a checkout with [bold]YOLO_REPO_ROOT[/bold] — the cwd is never\n" +
			"consulted:\n" +
			`  YOLO_REPO_ROOT=~/code/yolo-jail yolo …`)
	}
	return reporoot.Resolution{}, false
}

// reportFlakeSource announces WHICH flake this launch will build the jail image
// from, and what selected it.
//
// The answer used to be both invisible and cwd-dependent: one `yolo`, one config,
// resolving a live checkout in one directory and an install-time snapshot in the
// next, printing the same banner either way — the banner's version string is a
// `git describe` OF THE RESOLVED ROOT (version.Get), so it cannot tell the two
// apart. Dropping the cwd-walk (internal/reporoot) removed the surprise; this
// line removes the guessing that remained.
//
// It prints in Phase 1, BEFORE pack staging and the nix build, because its whole
// value is naming what the next few gigabytes are being built from while there is
// still time to Ctrl-C. The startup banner cannot serve: it is emitted after
// autoLoadImage has already built and streamed the image.
func (o *Options) reportFlakeSource(res reporoot.Resolution) {
	o.pr(o.Stderr).printf("[dim]Flake source: %s (%s)[/dim]", res.Root, res.Source.Describe())
}

// --- small filesystem helpers ---

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// expandUser expand a leading "~"/"~/…" against
// $HOME (or the passwd home). A "~user" form is left untouched.
func expandUser(p string) string {
	if len(p) == 0 || p[0] != '~' {
		return p
	}
	i := 1
	for i < len(p) && p[i] != '/' {
		i++
	}
	if i != 1 {
		return p // ~user form
	}
	home := homeDir()
	if home == "" {
		return p
	}
	return home + p[i:]
}

func homeDir() string {
	if h, ok := os.LookupEnv("HOME"); ok {
		if h == "" {
			return "/"
		}
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/"
}

// configRuntime returns config["runtime"] as a string, or "".
func configRuntime(cfg *jsonx.OrderedMap) string {
	if cfg == nil {
		return ""
	}
	v, _ := cfg.Get("runtime")
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func inStrSlice(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
