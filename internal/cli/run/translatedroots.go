package run

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
)

// translatedroots.go is both halves of docs/design/in-jail-nix-roots.md §4 as a launch uses
// them: the HOST PATH MAP a launch states for the jail it starts, and the ROOTER a launch
// uses for yolo's own GC roots (NR-D2) — nix-store on the host, a translated root in a jail.
//
// A TRANSLATED ROOT (coined in that doc) is an ordinary indirect nix GC root sent under the
// host's spelling of a link the jail made. A jail's own `nix-store --add-root` sends the
// jail's spelling, which the host daemon resolves on the host filesystem, finds missing and
// deletes as stale — which is why every in-jail root here used to be skipped outright.

// gcRooter is how this launch registers yolo's own nix GC roots: the image root
// (rootImageFn), the prefix root (jailPrefixSource) and the store-delivered profiles' roots
// (storepackages.go).
//
// On the host it is image.AddRoot. In a jail it is the translated root, through the map this
// jail's launcher stated in nixroots.MapEnv; with no usable map it is nil, and the caller
// registers nothing, which is what every jail did before this existed. A malformed map is the
// same nil, silently: the design's rule is that every failure degrades to today's behavior,
// and a launcher bug is not the jail's to report.
//
// macos-user reaches the nil arm too, its launcher stating no map: an indirect root there
// would be valid as made (the sandbox shares the host filesystem), but rooting it is not
// what this slice builds.
func (o *Options) gcRooter() image.Rooter {
	if !o.inJail() {
		return image.AddRoot
	}
	m, err := nixroots.ParseHostMap(o.Getenv(nixroots.MapEnv))
	if err != nil || !m.Translates() {
		return nil
	}
	return translatedRooter(nixroots.Registrar{
		Map: m,
		// nix's own client reads both variables, so a jail that points its nix elsewhere
		// points this at the same daemon and store.
		Socket:   o.Getenv("NIX_DAEMON_SOCKET_PATH"),
		StoreDir: o.Getenv("NIX_STORE_DIR"),
	})
}

// translatedRooter adapts a registrar to image.Rooter. Only the daemon's REFUSAL is said
// (§4, "Failure"): an untrusted client has always been allowed this operation, so a refusal
// means the host's nix changed the rule. Every other failure — a link under no mapped mount,
// no daemon, a protocol error — leaves the root unregistered and says nothing, exactly as a
// jail behaved before.
func translatedRooter(reg nixroots.Registrar) image.Rooter {
	return func(link, storePath string, out io.Writer, failMsg string) error {
		_, err := reg.Root(link, storePath)
		var rejected *nixroots.RejectedError
		if errors.As(err, &rejected) {
			fmt.Fprintln(out, "Warning: "+failMsg+": the host's nix daemon refused the root "+
				"("+rejected.Msg+")")
		}
		return err
	}
}

// hostGCRootsAutoMountArgs binds the host's /nix/var/nix/gcroots/auto read-only at
// nixroots.HostAutoDir, for the in-jail root watcher (nixroots/watch.go). It is the only
// view the watcher needs: the entries' NAMES and the path strings they point at, never a
// host file's contents (OQ-NR2, ruled with no launch line of its own).
//
// The caller emits it only with the host nix daemon mounted and never under the seal. A
// launcher in a jail binds its OWN view of the directory on, so a nested jail watches the
// same host directory through its composed map. No directory, no mount: the watcher then
// finds nothing bound and exits, the jail as it was without it.
func (o *Options) hostGCRootsAutoMountArgs() []string {
	src := nixroots.HostAutoSource
	if o.inJail() {
		src = nixroots.HostAutoDir
	}
	if !o.PathExists(src) {
		return nil
	}
	return []string{"-v", src + ":" + nixroots.HostAutoDir + ":ro"}
}

// hostPathMapEnvArgs is the `-e` pair stating this launch's host path map, computed from
// the argv itself — every mount the assembler emitted before the image ref — so the map
// cannot name a mount the jail does not have, or miss one it does.
//
// Emitted only when the jail gets the host nix daemon, the one thing that reads it, and
// never under the seal, which withholds that daemon (seal.go). Omitted, too, when no entry
// translates: an absent variable already means "nothing here translates".
//
// IN A JAIL the launcher's own sources are paths in ITS jail, so each is resolved and passed
// through the map this jail was given; a source that map cannot spell becomes a mask
// (nixroots.Compose). That is how the documented nested workspace, under /tmp, ends up
// translating nothing while the machine-scope binds under the jail's home still do.
func (o *Options) hostPathMapEnvArgs(rt string, sealed bool, argv []string) []string {
	if sealed || !o.hostNixMounted(rt) {
		return nil
	}
	var toHost func(string) (string, bool)
	if o.inJail() {
		parent, err := nixroots.ParseHostMap(o.Getenv(nixroots.MapEnv))
		if err != nil {
			parent = nixroots.HostMap{}
		}
		toHost = func(source string) (string, bool) { return parent.Translate(resolveSymlinks(source)) }
	}
	m := nixroots.Compose(argvMounts(argv), toHost)
	if !m.Translates() {
		return nil
	}
	return []string{"-e", nixroots.MapEnv + "=" + m.Encode()}
}

// argvMounts reads every mount a podman argv makes: `-v`/`--volume` (a volume when the
// source is not an absolute path, read-only for `ro`, and not the source's files for an
// overlay `O`), `--mount` (bind, volume or tmpfs, `readonly`/`ro`), and `--tmpfs`. Only the
// writable bind of an absolute source is Writable; every other mount is recorded so that it
// masks what lies under it.
//
// The argv must stop before the image ref — what follows it is the jailed command, whose
// arguments are not podman's. The assembler calls this before it appends the image.
func argvMounts(argv []string) []nixroots.Mount {
	var out []nixroots.Mount
	volume := func(spec string) {
		parts := strings.Split(spec, ":")
		var src, dst string
		var opts []string
		switch len(parts) {
		case 1: // an anonymous volume
			dst = parts[0]
		case 2:
			src, dst = parts[0], parts[1]
		default:
			src, dst, opts = parts[0], parts[1], strings.Split(parts[2], ",")
		}
		writable := strings.HasPrefix(src, "/") && !slices.Contains(opts, "ro") && !slices.Contains(opts, "O")
		out = append(out, nixroots.Mount{Dest: dst, Source: src, Writable: writable})
	}
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case (a == "-v" || a == "--volume") && i+1 < len(argv):
			i++
			volume(argv[i])
		case strings.HasPrefix(a, "--volume="):
			volume(strings.TrimPrefix(a, "--volume="))
		case a == "--tmpfs" && i+1 < len(argv):
			i++
			dst, _, _ := strings.Cut(argv[i], ":")
			out = append(out, nixroots.Mount{Dest: dst})
		case a == "--mount" && i+1 < len(argv):
			i++
			kind, src, dst, readonly := "", "", "", false
			for _, kv := range strings.Split(argv[i], ",") {
				k, v, _ := strings.Cut(kv, "=")
				switch k {
				case "type":
					kind = v
				case "source", "src":
					src = v
				case "target", "destination", "dst":
					dst = v
				case "readonly", "ro":
					readonly = v == "" || v == "true"
				}
			}
			writable := kind == "bind" && strings.HasPrefix(src, "/") && !readonly
			out = append(out, nixroots.Mount{Dest: dst, Source: src, Writable: writable})
		}
	}
	return out
}
