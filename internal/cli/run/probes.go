package run

import (
	"io"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// resolveRepoRoot locates the yolo-jail repo root for nix image builds. Returns
// (path, ok); ok=false is the exit(1) branch (with an actionable message printed
// to stderr). The resolution itself is the single shared method in
// internal/reporoot — identical inside and outside the jail — so run and check
// never drift. This wrapper adds only the run-side error banner (reporoot.Resolve
// is pure and never prints).
func resolveRepoRoot(getenv func(string) string, stderr io.Writer, color bool) (string, bool) {
	if root, ok := reporoot.Resolve(getenv); ok {
		return root, true
	}
	if stderr != nil {
		pr := printer{rt: richtext.Printer{W: stderr}}
		pr.print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
			"The yolo CLI needs the repo for nix image builds.\n\n" +
			"Fix: add [bold]repo_path[/bold] to ~/.config/yolo-jail/config.jsonc:\n" +
			`  { "repo_path": "~/code/yolo-jail" }`)
	}
	return "", false
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
