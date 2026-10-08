package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// nixroots.go is `yolo nix-roots`: the inspection and release surface of a workspace's
// MANAGED ROOTS (docs/design/in-jail-nix-roots.md §4.1, internal/nixroots/registry.go), and
// `keep`, the hand-run form of the root watcher (option A of §5).

const nixRootsUsage = `Usage: yolo nix-roots <list|keep|release|prune> [args]

Nix in a container jail builds through the HOST's nix daemon, and a link the jail
makes (./result, a profile generation, a nix-direnv cache) is not a root the host
honors: the host's garbage collector can delete what it points at between two
commands. yolo keeps such links alive for you with MANAGED ROOTS: a link of
yolo's own under <workspace>/.yolo/nix-roots, pointing at the same store path and
registered with the host's daemon under the host's spelling.

A managed root is bounded. It lives for 7 days after it was last renewed (a
rebuild of the same link, or a keep), a workspace holds at most 64 (admitting
one more releases the least recently renewed), and it is released as soon as
its link is deleted. Releasing one deletes only yolo's link; your own link stays,
and the host's next garbage collection may then collect what it points at.

Subcommands:
  list [--format json|--json]
                           This workspace's managed roots: the link, the store
                           path, when it was renewed and when it expires.
  keep <link>...           In a jail: protect these links now. Each must be a
                           symlink straight into /nix/store, under the workspace
                           or your home (not /tmp, which is scratch).
  release <id|link>...     Release these roots; --all releases every one.
  prune                    Apply the lifecycle now: expired leases, links that
                           are gone, the cap. Runs on the host too.

  --help, -h  Show this help.

Examples:
  yolo nix-roots list
  yolo nix-roots keep ./result
  yolo nix-roots release ./result
  yolo nix-roots release --all`

func runNixRoots(args []string) int {
	if answerHelp("nix-roots", args, os.Stdout) {
		return 0
	}
	env := nixRootsEnv{inJail: config.InJail(), getenv: os.Getenv, now: time.Now}
	if env.inJail {
		env.workspace = config.JailWorkspace()
	} else if ws, ok := resolveWorkspaceRoot(); ok {
		env.workspace = ws
	}
	return nixRootsMain(args, env, os.Stdout, os.Stderr)
}

// nixRootsEnv is what the command reads from the process, split out so a test can stand in.
type nixRootsEnv struct {
	inJail    bool
	workspace string // "" when the host found none
	getenv    func(string) string
	now       func() time.Time
}

// registry builds the workspace's registry for this side. In a jail it can register a
// root only when the launcher stated a host path map (nixroots.MapEnv); register reports
// whether it can.
func (e nixRootsEnv) registry() (reg *nixroots.Registry, m nixroots.HostMap, registrar *nixroots.Registrar) {
	reg = &nixroots.Registry{Workspace: e.workspace, Now: e.now, StoreDir: e.getenv("NIX_STORE_DIR"),
		SourceSide: nixroots.SideHost}
	if !e.inJail {
		return reg, nil, nil
	}
	reg.SourceSide = nixroots.SideJail
	m, err := nixroots.ParseHostMap(e.getenv(nixroots.MapEnv))
	if err != nil || !m.Translates() {
		return reg, nil, nil
	}
	registrar = &nixroots.Registrar{Map: m, Socket: e.getenv("NIX_DAEMON_SOCKET_PATH"),
		StoreDir: e.getenv("NIX_STORE_DIR")}
	reg.Register = registrar.Register
	return reg, m, registrar
}

func nixRootsMain(args []string, env nixRootsEnv, out, errw io.Writer) int {
	rest := args
	if len(rest) > 0 && rest[0] == "nix-roots" {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		fmt.Fprintln(errw, "yolo nix-roots: name a subcommand: list, keep, release or prune "+
			"(`yolo nix-roots --help` says what each does)")
		return 2
	}
	if env.workspace == "" {
		fmt.Fprintln(errw, "yolo nix-roots: no workspace here. Run it from inside a workspace yolo "+
			"has launched (one with a .yolo directory or a yolo-jail.jsonc).")
		return 1
	}
	sub, flags := rest[0], rest[1:]
	reg, m, registrar := env.registry()
	switch sub {
	case "list", "ls":
		format, ok := parseOutputFormat("nix-roots", flags, errw)
		if !ok || refuseUnknownFlags("nix-roots list", flags, []string{"--format", "--json"}, errw) {
			return 2
		}
		return nixRootsList(reg, format, out, errw)
	case "keep":
		if refuseUnknownFlags("nix-roots keep", flags, nil, errw) {
			return 2
		}
		if len(flags) == 0 {
			fmt.Fprintln(errw, "yolo nix-roots keep: name the links to keep, e.g. `yolo nix-roots keep ./result`")
			return 2
		}
		if !env.inJail {
			fmt.Fprintln(errw, "yolo nix-roots keep: on the host a link is already a root nix "+
				"honors; make one with `nix-store --add-root <link> -r <store-path>`. keep is for a jail.")
			return 2
		}
		if registrar == nil {
			fmt.Fprintln(errw, "yolo nix-roots keep: this jail's launcher stated no host path map, "+
				"so no root made here can be spelled for the host. That is a jail without the host "+
				"nix daemon, a macos-user jail (where `nix-store --add-root` already makes a root "+
				"the host honors), or a launcher older than the map: restart the jail with a current yolo.")
			return 1
		}
		return nixRootsKeep(reg, registrar, m, flags, out, errw)
	case "release", "rm":
		if refuseUnknownFlags("nix-roots release", flags, []string{"--all"}, errw) {
			return 2
		}
		all := false
		var which []string
		for _, a := range flags {
			if a == "--all" {
				all = true
			} else {
				which = append(which, a)
			}
		}
		if !all && len(which) == 0 {
			fmt.Fprintln(errw, "yolo nix-roots release: name roots by id or link (see `yolo nix-roots list`), or pass --all")
			return 2
		}
		released, missing, err := reg.Release(which, all)
		printReleased(out, released)
		for _, w := range missing {
			fmt.Fprintf(errw, "yolo nix-roots release: no managed root is %s; `yolo nix-roots list` shows them\n", w)
		}
		if err != nil {
			return nixRootsFail(errw, "release", err)
		}
		if len(released) == 0 && len(missing) == 0 {
			fmt.Fprintln(out, "No managed roots to release.")
		}
		if len(missing) > 0 {
			return 1
		}
		return 0
	case "prune":
		if refuseUnknownFlags("nix-roots prune", flags, nil, errw) {
			return 2
		}
		released, err := reg.Prune()
		printReleased(out, released)
		if err != nil {
			return nixRootsFail(errw, "prune", err)
		}
		if len(released) == 0 {
			fmt.Fprintln(out, "Nothing to release: every managed root is within its lease and the cap.")
		}
		return 0
	default:
		fmt.Fprintf(errw, "yolo nix-roots: unknown subcommand %q: use list, keep, release or prune\n", sub)
		return 2
	}
}

func nixRootsFail(errw io.Writer, verb string, err error) int {
	fmt.Fprintf(errw, "yolo nix-roots %s: %v\n", verb, err)
	if errors.Is(err, fs.ErrPermission) {
		fmt.Fprintln(errw, "The workspace's .yolo/nix-roots is not writable here; run the command where the workspace is writable.")
	}
	return 1
}

func printReleased(out io.Writer, released []nixroots.Released) {
	for _, r := range released {
		name := r.Root.Source
		if name == "" {
			name = "(unrecorded link " + r.Root.ID + ")"
		}
		fmt.Fprintf(out, "Released %s %s: %s\n", r.Root.ID, termsafe.Visible(name), r.Reason)
	}
}

type nixRootJSON struct {
	nixroots.Root
	Expires time.Time `json:"expires"`
}

func nixRootsList(reg *nixroots.Registry, format string, out, errw io.Writer) int {
	roots, err := reg.List()
	if err != nil {
		return nixRootsFail(errw, "list", err)
	}
	lease := reg.EffectiveLease()
	if format == outfmt.JSON {
		rows := make([]nixRootJSON, 0, len(roots))
		for _, r := range roots {
			rows = append(rows, nixRootJSON{Root: r, Expires: r.Expires(lease)})
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			return nixRootsFail(errw, "list", err)
		}
		return 0
	}
	if len(roots) == 0 {
		fmt.Fprintln(out, "No managed nix roots in this workspace.")
		return 0
	}
	fmt.Fprintf(out, "%d of %d managed nix roots (each lives %s after its last renewal):\n",
		len(roots), reg.EffectiveCap(), humanLease(lease))
	for _, r := range roots {
		// Every string here is from roots.json, which the jail writes (termsafe).
		fmt.Fprintf(out, "  %s  %s\n      -> %s\n      by %s, renewed %s, expires %s\n",
			r.ID, termsafe.Visible(r.Source), termsafe.Visible(r.Target), termsafe.Visible(r.By), r.Renewed.Local().Format("2006-01-02 15:04"),
			r.Expires(lease).Local().Format("2006-01-02 15:04"))
	}
	return 0
}

func humanLease(d time.Duration) string {
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%d days", int(d/(24*time.Hour)))
	}
	return d.String()
}

// nixRootsKeep admits each link: a symlink straight into the store, under a mount the map
// can spell for the host.
func nixRootsKeep(reg *nixroots.Registry, registrar *nixroots.Registrar, m nixroots.HostMap, links []string, out, errw io.Writer) int {
	rc := 0
	for _, l := range links {
		src, err := filepath.Abs(l)
		if err != nil {
			fmt.Fprintf(errw, "yolo nix-roots keep: %s: %v\n", l, err)
			rc = 1
			continue
		}
		target, err := os.Readlink(src)
		if err != nil {
			fmt.Fprintf(errw, "yolo nix-roots keep: %s is not a symlink (%v); keep takes the link nix made, e.g. ./result\n", l, err)
			rc = 1
			continue
		}
		if !filepath.IsAbs(target) || !reg.IsStorePath(target) {
			fmt.Fprintf(errw, "yolo nix-roots keep: %s points at %s, not straight into the store; "+
				"keep the link it names instead (for a profile, its numbered generation link)\n", l, target)
			rc = 1
			continue
		}
		dir, err := filepath.EvalSymlinks(filepath.Dir(src))
		if err != nil {
			fmt.Fprintf(errw, "yolo nix-roots keep: %s: %v\n", l, err)
			rc = 1
			continue
		}
		host, ok := m.Translate(filepath.Join(dir, filepath.Base(src)))
		if !ok {
			fmt.Fprintf(errw, "yolo nix-roots keep: %s is under no directory the host can see "+
				"(/tmp and /var/tmp are scratch the jail discards); move the link under the "+
				"workspace or your home and keep it there\n", l)
			rc = 1
			continue
		}
		adm, err := keepOne(reg, registrar, src, host, target)
		printReleased(out, adm.Released)
		if err != nil {
			var rej *nixroots.RejectedError
			switch {
			case errors.As(err, &rej):
				fmt.Fprintf(errw, "yolo nix-roots keep: %s: %v. The host's nix no longer accepts "+
					"this root from a jail; run `nix-store --add-root` on the host instead.\n", l, err)
			case strings.Contains(err.Error(), "does not point into the store"):
				fmt.Fprintf(errw, "yolo nix-roots keep: %v; keep only a link nix made into the store\n", err)
			default:
				fmt.Fprintf(errw, "yolo nix-roots keep: %s: %v. Check that the host nix daemon "+
					"answers (`nix store info`), then try again.\n", l, err)
			}
			rc = 1
			continue
		}
		verb := "Renewed"
		if adm.New {
			verb = "Kept"
		}
		fmt.Fprintf(out, "%s %s -> %s (id %s, until %s)\n", verb, src, target, adm.Root.ID,
			adm.Root.Expires(reg.EffectiveLease()).Local().Format("2006-01-02 15:04"))
	}
	return rc
}

// keepOne admits one link with its target pinned for the whole admission (nixroots.Pin), the
// same handoff the watcher makes.
func keepOne(reg *nixroots.Registry, registrar *nixroots.Registrar, src, host, target string) (nixroots.Admission, error) {
	pin, err := registrar.Pin(target)
	if err != nil {
		return nixroots.Admission{}, err
	}
	defer pin.Close()
	pinned := *reg
	pinned.Register = pin.Register
	return pinned.Admit(src, host, target, nixroots.ByKeep)
}
