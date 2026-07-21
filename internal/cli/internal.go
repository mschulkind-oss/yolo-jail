package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerrelay"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/hostprocesses"
	"github.com/mschulkind-oss/yolo-jail/internal/journald"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/oauthbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/repopath"
)

// runInternal dispatches the hidden `yolo internal <cmd>` family — debugging
// tooling and the in-process host-daemon entry points. This group is
// deliberately kept OUT of the dispatch registry (the documented CLI surface)
// and intercepted before RewriteArgv, so it never participates in `--`->run
// rewrite semantics.
func runInternal(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal <config-dump|daemon|darwin-bootstrap|migrate-host|write-repo-path> [args...]")
		return 2
	}
	switch args[0] {
	case "config-dump":
		return runConfigDump(args[1:])
	case "daemon":
		return runInternalDaemon(args[1:])
	case "darwin-bootstrap":
		return runDarwinBootstrap(args[1:])
	case "migrate-host":
		return runMigrateHost(args[1:])
	case "write-repo-path":
		return runWriteRepoPath(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yolo internal: unknown command %q\n", args[0])
		return 2
	}
}

// runDarwinBootstrap is the self-exec target the macos-user launch stages and
// runs AS the sandbox user (J2 §3): `sudo --user=_yolojail … /var/yolo-jail/yolo
// internal darwin-bootstrap`. It replaces the old generated-Python bootstrap
// that imported the deleted src/ tree. It self-sets JAIL_HOME/HOME (sudo without
// --set-home is not a reliable HOME source), builds an *entrypoint.Env pointed
// at the sandbox home + real workspace, and runs the native generation entry.
//
// Inputs arrive as env vars the launcher bakes into the `env -i K=V…` argv
// (matching how the launch env already crosses into the sandbox): the git/jj
// identity + YOLO_* generator contract ride through verbatim; the three darwin
// extras are YOLO_DARWIN_WORKSPACE, YOLO_DARWIN_MACOS_LOG, and
// YOLO_DARWIN_LOGIN_PATH.
func runDarwinBootstrap(_ []string) int {
	home := firstNonEmptyEnv("JAIL_HOME", "HOME")
	if home == "" {
		home = macosuser.SandboxHome()
	}
	// Rebind HOME/JAIL_HOME before Env resolves its home-derived paths.
	os.Setenv("JAIL_HOME", home)
	os.Setenv("HOME", home)

	e := entrypoint.EnvFromOS()
	e.Stderr = os.Stderr
	e.Home = home
	// Native platform values (J2 §1 seams): real workspace, macOS shim bin,
	// BSD stat.
	if ws := os.Getenv("YOLO_DARWIN_WORKSPACE"); ws != "" {
		e.Workspace = ws
	}
	e.ShimBinDir = "/usr/bin"
	e.GNUStat = false

	opts := entrypoint.DarwinBootstrapOptions{
		MacosLog:      os.Getenv("YOLO_DARWIN_MACOS_LOG"),
		LoginPath:     os.Getenv("YOLO_DARWIN_LOGIN_PATH"),
		YoloLogScript: macosuser.MacosLogWrapperScript(os.Getenv("YOLO_DARWIN_MACOS_LOG")),
	}
	entrypoint.RunDarwinBootstrap(e, opts)
	fmt.Println("yolo-jail macos-user bootstrap ok")
	return 0
}

// firstNonEmptyEnv returns the first environment variable in keys with a
// non-empty value, or "".
func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// runInternalDaemon dispatches the hidden `yolo internal daemon <name>` group —
// the four host daemons, callable in-process so a single yolo binary can serve
// as each one. The remaining argv is passed through verbatim, so each daemon's
// flag surface (--socket, --self-check, --init-ca, …) is byte-identical to its
// standalone binary.
func runInternalDaemon(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo internal daemon <claude-oauth-broker|host-processes|broker-relay|journal> [args...]")
		return 2
	}
	rest := args[1:]
	switch args[0] {
	case "claude-oauth-broker":
		return oauthbroker.Main(rest)
	case "host-processes":
		return hostprocesses.Main(rest)
	case "broker-relay":
		return brokerrelay.Main(rest)
	case "journal":
		return journald.Main(rest)
	default:
		fmt.Fprintf(os.Stderr, "yolo internal daemon: unknown daemon %q\n", args[0])
		return 2
	}
}

// runMigrateHost retires host-side artifacts left by the pre-Go (Python)
// distribution, so `go install ./cmd/yolo` can land its binary. The Justfile
// `install` recipe runs it through `go run` immediately before `go install` —
// it cannot live in the installed binary's startup path, because the whole
// point is to unblock the install that produces that binary.
//
// Flags: --gobin=DIR (default: $GOBIN, else $GOPATH/bin).
func runMigrateHost(args []string) int {
	gobin := ""
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--gobin="):
			gobin = strings.TrimPrefix(a, "--gobin=")
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "migrate-host: unknown flag %q\n", a)
			return 2
		default:
			fmt.Fprintf(os.Stderr, "migrate-host: unexpected argument %q\n", a)
			return 2
		}
	}

	if gobin == "" {
		resolved, err := hostmigrate.DefaultGOBIN()
		if err != nil {
			fmt.Fprintln(os.Stderr, "migrate-host:", err)
			return 1
		}
		gobin = resolved
	}

	if _, err := hostmigrate.New(gobin).Preflight(); err != nil {
		fmt.Fprintf(os.Stderr, "\nyolo-jail: cannot install over an existing file.\n  %v\n", err)
		return 1
	}
	return 0
}

// runWriteRepoPath records the yolo-jail source-checkout path in the user
// config's repo_path key, so an installed `yolo` can find the repo for nix
// image builds from any directory (the Go analog of the Python wheel's bundled
// source; see docs/research/repo-root-and-distribution.md). The Justfile
// `deploy` recipe runs it (via `go run`) with the checkout dir as the argument.
// Idempotent and comment-preserving.
//
// Usage: yolo internal write-repo-path <repo-dir>
func runWriteRepoPath(args []string) int {
	repoDir := ""
	for _, a := range args {
		if len(a) > 0 && a[0] == '-' {
			fmt.Fprintf(os.Stderr, "write-repo-path: unknown flag %q\n", a)
			return 2
		}
		if repoDir != "" {
			fmt.Fprintf(os.Stderr, "write-repo-path: unexpected extra argument %q\n", a)
			return 2
		}
		repoDir = a
	}
	if repoDir == "" {
		fmt.Fprintln(os.Stderr, "usage: yolo internal write-repo-path <repo-dir>")
		return 2
	}
	if !fileExistsCLI(filepath.Join(repoDir, "flake.nix")) {
		fmt.Fprintf(os.Stderr, "write-repo-path: %s has no flake.nix — not a yolo-jail checkout\n", repoDir)
		return 1
	}
	if err := repopath.WriteFile(paths.UserConfigPath(), repoDir, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "write-repo-path:", err)
		return 1
	}
	return 0
}

func fileExistsCLI(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// runConfigDump loads + merges the config for a workspace (default: cwd) via
// internal/config and prints the merged config as canonical snapshot JSON,
// followed by the validation errors/warnings. Used for differential testing
// and for eyeballing the merged shape.
//
// Flags: --strict (raise on malformed config), positional workspace dir.
func runConfigDump(args []string) int {
	strict := false
	workspace := ""
	for _, a := range args {
		switch {
		case a == "--strict":
			strict = true
		case len(a) > 0 && a[0] == '-':
			fmt.Fprintf(os.Stderr, "config-dump: unknown flag %q\n", a)
			return 2
		default:
			workspace = a
		}
	}
	if workspace == "" {
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	}

	cfg, err := config.LoadConfig(workspace, strict, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	errs, warns := config.ValidateConfig(cfg, workspace, nil)

	out := jsonx.NewOrderedMap()
	out.Set("config", cfg)
	out.Set("errors", strAny(errs))
	out.Set("warnings", strAny(warns))
	snap, err := config.SnapshotJSON(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config-dump:", err)
		return 1
	}
	fmt.Println(snap)
	if len(errs) > 0 {
		return 1
	}
	return 0
}

func strAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
