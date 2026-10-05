package cli

// hostblockers.go puts blocked tools at the head of the PATH of the program `yolo host --`
// starts (docs/design/host-launch-environment.md HE-D11): the selected packs' `blocked-tool`
// contributions (the guardrails pack's grep and find) and the user-scope `security.blocked_tools`,
// rendered by the jail's own writer (entrypoint.RenderBlockers) into a directory yolo keeps on the
// host, first on the child's PATH.
//
// WHY THE HOST CAN DO THIS NOW. The census used to refuse the kind off-container because "yolo
// owns no PATH entry to put one in". Since HE-D1, `yolo host --` composes the child's PATH itself
// (hostChildPath: the launch PATH, then the floor's bin/), so for this verb yolo does own one, for
// that process only. `yolo host apply` still launches nothing and owns no PATH, so the kind stays
// unbuilt there (render.HostUnimplemented).
//
// WHAT IT CANNOT PROMISE, and the disclosure says so every launch: the block is a PATH lookup's
// first hit, so a shell that resets PATH (a login shell on macOS runs path_helper, Debian's
// /etc/profile sets PATH outright) or an agent's own shell function of the same name never meets
// it — MEASURED in a jail on 2026-10-04: Claude Code 2.1.289's Bash tool defines `grep` and `find`
// as functions, so its direct commands skip the jail's blockers too. YOLO_BYPASS_SHIMS=1 lets a
// command through, as in a jail.
//
// WHICH CONFIG: the user scope alone, as every input `yolo host` composes from. A workspace's
// yolo-jail.jsonc is the repository's, and a repository choosing what the user's own shell tools
// do outside every sandbox is not a declaration it gets to make (UserScopeConfig).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostBlockers is what one launch's blocked tools came to.
type hostBlockers struct {
	// path is the child's PATH: the block dir first and every other block dir taken out when
	// anything is blocked, else exactly the PATH it was handed.
	path string
	// dir is the content-addressed block dir, "" when nothing is blocked.
	dir string
	// notes are the entries left unblocked and why, one line each.
	notes []string
	// disclosure is the launch's one statement of what it blocks, nil when nothing is.
	disclosure []string
}

// hostBlockedChildPath is childPath with this launch's blocked tools first on it, printing the
// notes and the disclosure. It is the one call hostLaunch makes, after the target resolved.
func hostBlockedChildPath(launch *hostComposition, childPath string, errw io.Writer) string {
	b := composeHostBlockers(launch.cfg, launch.packs, childPath)
	for _, n := range b.notes {
		fmt.Fprintf(errw, "yolo host: %s\n", n)
	}
	printHostLines(errw, b.disclosure)
	return b.path
}

// composeHostBlockers renders and writes the blockers for one launch whose child would get
// childPath. In a jail it does nothing: the jail's own blockers are already first on the PATH a
// nested `yolo host` inherits, and childEnviron hands the child that PATH unchanged.
func composeHostBlockers(cfg *jsonx.OrderedMap, packs []*packload.Pack, childPath string) hostBlockers {
	out := hostBlockers{path: childPath}
	if config.InJail() {
		return out
	}
	security := userSecuritySection(cfg)
	packTools := packload.BlockedTools(packs)
	entries := config.NormalizeBlockedToolsWith(security, packTools)
	if len(entries) == 0 {
		return out
	}
	scripts := entrypoint.RenderBlockers(entries, entrypoint.BlockerRender{
		// The replacement rule and the real grep/find read the PATH the child will have,
		// skipping yolo's own folders (a stale block dir from an outer launch among them), so
		// a shim is never its own replacement nor the binary it passes a call on to.
		LookPath: func(bin string) string {
			p, err := resolveHostTarget(childPath, bin)
			if err != nil {
				return ""
			}
			return p
		},
		RealBin: func(name string) (string, bool) {
			p, err := resolveHostTarget(childPath, name)
			if err != nil {
				out.notes = append(out.notes, fmt.Sprintf("not blocking %s: there is no %s on "+
					"this launch's PATH to block", name, name))
				return "", false
			}
			return p, true
		},
		NoReplacement: func(name, repl string) {
			out.notes = append(out.notes, fmt.Sprintf("not blocking %s: its replacement %s is "+
				"not on this launch's PATH, and a block with no working alternative removes the "+
				"capability instead of redirecting it (install %s, or name its folder in "+
				"`host_path`, to restore the block)", name, repl, repl))
		},
		Warn: func(msg string) { out.notes = append(out.notes, msg) },
	})
	if len(scripts) == 0 {
		return out
	}
	dir, err := writeHostBlockDir(paths.HostBlockDir(), scripts)
	if err != nil {
		out.notes = append(out.notes, fmt.Sprintf("could not write the blocked-tool shims "+
			"(%v), so this launch blocks nothing; `yolo check` reports the state dir's problems", err))
		return out
	}
	out.dir = dir
	out.path = blockDirFirst(dir, paths.HostBlockDir(), childPath)
	out.disclosure = hostBlockerDisclosure(scripts, blockerOrigins(security, packTools), dir)
	return out
}

// userSecuritySection is the user-scope config's `security` object, nil when there is none.
func userSecuritySection(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	v, _ := cfg.Get("security")
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// blockerOrigins names who declared each blocked name: the pack, or the user's own list, whose
// entry replaces a pack's of the same name whole (config.NormalizeBlockedToolsWith).
func blockerOrigins(security *jsonx.OrderedMap, packTools []packload.BlockedTool) map[string]string {
	out := map[string]string{}
	for _, t := range packTools {
		if _, ok := out[t.Name]; !ok {
			out[t.Name] = "the " + t.Pack + " pack"
		}
	}
	for _, e := range config.NormalizeBlockedTools(security) {
		if m, ok := e.(*jsonx.OrderedMap); ok {
			if n, ok := m.Get("name"); ok {
				if s, ok := n.(string); ok {
					out[s] = "your security.blocked_tools"
				}
			}
		}
	}
	return out
}

// hostBlockerDisclosure is the launch's one statement of its blockers: what is blocked and who
// asked, where the shims sit, the hatch, and the two ways round them a launch cannot close. A
// launch has no quiet mode (OQ-RO3), and a block the user does not know about reads as a broken
// tool.
func hostBlockerDisclosure(scripts []entrypoint.BlockerScript, origins map[string]string, dir string) []string {
	var order []string
	byOrigin := map[string][]string{}
	for _, s := range scripts {
		o := origins[s.Name]
		if o == "" {
			o = "your security.blocked_tools"
		}
		if _, ok := byOrigin[o]; !ok {
			order = append(order, o)
		}
		byOrigin[o] = append(byOrigin[o], s.Name)
	}
	groups := make([]string, 0, len(order))
	for _, o := range order {
		groups = append(groups, strings.Join(byOrigin[o], ", ")+" ("+o+")")
	}
	return []string{
		fmt.Sprintf("blocking %s for this launch: %s is first on its PATH, and "+
			"YOLO_BYPASS_SHIMS=1 lets a command through.", strings.Join(groups, "; "), homeTilde(dir)),
		"  A shell that resets PATH (a login shell: macOS's path_helper, Debian's /etc/profile) " +
			"or an agent's own shell function of the same name (Claude Code's Bash tool defines " +
			"grep and find) does not meet these blocks.",
	}
}

// blockDirFirst is childPath with dir first and every other entry under root (another launch's
// block dir, handed down by a nested launch) taken out.
func blockDirFirst(dir, root, childPath string) string {
	out := []string{dir}
	root = filepath.Clean(root)
	for _, e := range filepath.SplitList(childPath) {
		if e == "" {
			continue
		}
		c := filepath.Clean(e)
		if c == root || strings.HasPrefix(c, root+string(os.PathSeparator)) {
			continue
		}
		out = append(out, e)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// writeHostBlockDir writes scripts into root/<digest of the scripts> and returns that directory.
// A directory of that name already there is reused as it is: its name is its content, and nothing
// but this function writes under root (no jail mounts it, paths.HostBlockDir). A new one is
// written beside it under a temporary name and renamed into place whole, so a launch never sees a
// half-written set, and two launches racing to write the same set both end on one directory.
func writeHostBlockDir(root string, scripts []entrypoint.BlockerScript) (string, error) {
	dir := filepath.Join(root, blockerDigest(scripts))
	if fi, err := os.Lstat(dir); err == nil && fi.IsDir() {
		return dir, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(root, ".tmp-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }() // a no-op once the rename moved it
	for _, s := range scripts {
		p := filepath.Join(tmp, s.Name)
		if err := os.WriteFile(p, []byte(s.Content), 0o755); err != nil {
			return "", err
		}
		// Explicit, for writeExecutable's reason: WriteFile's mode passes through the umask.
		if err := os.Chmod(p, 0o755); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if fi, lerr := os.Lstat(dir); lerr == nil && fi.IsDir() {
			return dir, nil // a concurrent launch wrote the same set first
		}
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("%s exists and is not a directory", dir)
		}
		return "", err
	}
	return dir, nil
}

// blockerDigest names a set of scripts by its content: every name and body, in name order.
func blockerDigest(scripts []entrypoint.BlockerScript) string {
	sorted := append([]entrypoint.BlockerScript(nil), scripts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	h := sha256.New()
	for _, s := range sorted {
		_, _ = io.WriteString(h, s.Name+"\x00"+s.Content+"\x00")
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
