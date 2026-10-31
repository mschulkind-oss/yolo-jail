package cli

// packinitplugin.go is `yolo pack init --from-plugin <dir>`: wrap an EXISTING agent plugin
// as a yolo pack, in one command.
//
// This exists because "you can pull in a plugin" and "pulling in a plugin is trivial" are
// different products. The mechanics are small — a pack.toml with one `skills` contribution and
// the plugin tree under skills/ — but they are also exactly the kind of thing nobody derives
// correctly from a design doc: the plugin has to sit UNDER skills/ or a jail never sees it. So
// yolo writes it.
//
// It COPIES the plugin tree in rather than symlinking it, and that is forced rather than
// chosen: packstage refuses a symlink pointing outside the pack root (a pack comes from
// someone else's repo, so an escaping link could stage a host secret), so a linked plugin
// produces a pack that fails `pack lint` and stages into no jail. A scaffold that does not
// lint is not a scaffold. The cost is that the copy is a fork — re-run `init --from-plugin`
// over the same pack after updating the plugin, which reports each file it refreshes.
//
// What it deliberately does NOT do is resolve a MARKETPLACE. yolo has its own fetch/lock/
// approve pipeline (internal/packsrc), and a plugin pulled in as a pack is pinned the way
// every other pack is pinned. Marrying two registries — two version models, two trust models
// — is a separate design, not a flag on init.

import (
	"encoding/json"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// packInitFromPlugin scaffolds a wrapper pack at packRoot around the plugin at pluginDir.
//
// name is the WRAPPER pack's name (its directory); the plugin keeps its own name, because that
// is what the tools namespace its skills by — renaming it here would change every invocation
// the user already knows.
func packInitFromPlugin(pluginDir, packRoot, name string, out, errw io.Writer) int {
	pluginAbs, err := filepath.Abs(pluginDir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: %v\n", err)
		return 1
	}
	plugin, ok := pluginpack.Load(pluginAbs)
	if !ok {
		// Name the file that was looked for. "not a plugin" sends someone to re-read docs;
		// "this path has no plugin.json" sends them to `ls`.
		fmt.Fprintf(errw, "yolo pack init: %s is not an agent plugin — no %s/plugin.json "+
			"there (a plugin directory carries its own manifest; that is what makes it "+
			"loadable in place)\n", pluginAbs, pluginpack.PreferredManifestDir)
		return 1
	}
	pluginName := plugin.Name()

	// The wrapper's pack.json. ONE skills contribution: `from: "skills"` puts the plugin under the
	// pack's skills/ dir, which is the layout both notches read, and both recognize the plugin
	// inside it and copy the tree verbatim at its delivered name. No `skills_tier`: what the
	// plugin is called is the namespace the user's config gives the pack, never the pack's
	// (docs/design/pack-naming.md PN-D9).
	decl := map[string]any{
		"name":        name,
		"description": wrapperDescription(pluginName, plugin.Manifest.Description),
		"contributes": []any{
			map[string]any{
				"kind": "skills",
				"from": pluginpack.SkillsSubdir,
				// The DESTINATION is the user's own choice and there is no good default that
				// is not a guess about which tool they use, so init writes claude's and says
				// so in the README. A wrong guess here is visible and one edit to fix; the
				// alternative (no destination) does not validate.
				"into": ".claude/skills",
			},
		},
	}
	body, err := json.MarshalIndent(decl, "", "  ")
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: %v\n", err)
		return 1
	}
	// Written as pack.toml, the format `yolo pack init` scaffolds for a plain pack: the declaration
	// is built as JSON above, then decoded and re-encoded through packdecl, so the file is the
	// encoder's own and the strict TOML reader accepts it.
	manifest, problems := packdecl.Decode(body)
	if manifest == nil || len(problems) > 0 {
		fmt.Fprintf(errw, "yolo pack init: the wrapper declaration is invalid: %v\n", problems)
		return 1
	}
	tomlBody, err := packdecl.EncodeTOML(manifest)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: %v\n", err)
		return 1
	}
	if rc := writeScaffoldFile(packRoot, "pack.toml", string(tomlBody), out, errw); rc != 0 {
		return rc
	}

	// Copy the plugin tree into skills/<plugin-name>/. The plugin's OWN name, not the pack's:
	// the destination directory name is what the tools qualify its skills with, so renaming it
	// here would silently change every invocation the plugin documents.
	treeRel := filepath.Join(pluginpack.SkillsSubdir, pluginName)
	wrote, kept, err := copyPluginTree(pluginAbs, filepath.Join(packRoot, treeRel))
	if err != nil {
		fmt.Fprintf(errw, "yolo pack init: copying the plugin: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "  create %s/ (%d file(s) from the plugin)\n", treeRel, wrote)
	if kept > 0 {
		fmt.Fprintf(out, "  skip   %s/ (%d file(s) already present, left as they are)\n",
			treeRel, kept)
	}

	if rc := writeScaffoldFile(packRoot, "README.md",
		pluginWrapperReadme(name, pluginName, packRoot, pluginAbs), out, errw); rc != 0 {
		return rc
	}

	// Report what the plugin carries, with the code-running components called out. This is
	// the moment the user is deciding whether to trust the thing, so it is the moment to say
	// what it does — not at the next launch's disclosure banner, and certainly not at first
	// apply. Each line names where the component sits, because one found at its default
	// location (hooks/hooks.json, bin/, …) is named nowhere in the manifest the user may have
	// read.
	fmt.Fprintf(out, "\nWrapped plugin %s (from %s)\n", pluginName, pluginAbs)
	comps := plugin.Components()
	if len(comps) == 0 {
		fmt.Fprintf(out, "  carries skills only.\n")
	} else {
		fmt.Fprintf(out, "  carries:\n")
		for _, c := range comps {
			flag := ""
			if c.RunsCode {
				flag = "   ⚠ RUNS CODE"
			}
			fmt.Fprintf(out, "    %-14s %s%s  (%s)\n", c.Name, c.Detail, flag,
				strings.Join(c.Sources, ", "))
		}
	}
	if plugin.RunsCode() {
		fmt.Fprintf(out, "\n  Those components run code on your behalf. They are DELIVERED with "+
			"the plugin tree (the tool loads the plugin); where a folder yolo did not write "+
			"holds the plugin's name, they are refused by name. Nothing asks you to approve them later, from a git "+
			"address or anywhere else: selecting the pack in your config is the consent, and "+
			"`yolo pack footprint` is where to review what it runs.\n")
	}
	if skills := plugin.SkillDirs(); len(skills) > 0 {
		fmt.Fprintf(out, "\n  Its skills will invoke as /<namespace>-%s:<skill>, or /%s:<skill> "+
			"from a bare entry (%d skill(s)).\n", packname.Normalize(pluginName), pluginName, len(skills))
	}
	fmt.Fprintf(out, "\nPack scaffolded at %s\nNext: yolo pack lint %s\n", packRoot, packRoot)
	return 0
}

// copyPluginTree copies the plugin tree into the pack, returning how many files it wrote and
// how many it left alone because they already existed.
//
// NEVER CLOBBERS, matching `init`'s contract that re-running is safe: someone who edited a
// wrapped skill must not lose it to a second `init`. That does mean a refresh needs the subtree
// deleted first, which the README says.
//
// The exec bit is PRESERVED. It used to be dropped (every file landed 0o644) because a pack
// carrying an executable could not stage without the consumer's `allow_exec`, so scaffolding
// one produced a pack that was refused. That gate is gone, and dropping the bit now would do
// the opposite of what it was for: silently break the plugin's own scripts in the pack
// generated from it, and leave the author to rediscover chmod.
//
// Symlinks INSIDE the tree are dereferenced for a related reason: packstage refuses one that
// escapes the pack, and a link's target is a fact about the plugin author's checkout rather
// than content the pack should carry.
func copyPluginTree(src, dst string) (wrote, kept int, err error) {
	err = filepath.Walk(src, func(path string, fi os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		// The plugin's own VCS metadata is never content, and copying it would put a second
		// .git tree inside the pack — the same rule packstage applies when staging.
		if fi.IsDir() {
			if filepath.Base(rel) == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		// Resolve a symlink to its content; a dangling one is skipped rather than fatal (it
		// stages nothing useful either way, and refusing would block an otherwise fine tree).
		if fi.Mode()&os.ModeSymlink != 0 {
			resolved, serr := os.Stat(path)
			if serr != nil || resolved.IsDir() {
				return nil
			}
		}
		if _, serr := os.Lstat(target); serr == nil {
			kept++
			return nil
		}
		data, derr := os.ReadFile(path)
		if derr != nil {
			return derr
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if fi.Mode().Perm()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(target, data, mode); err != nil {
			return err
		}
		wrote++
		return nil
	})
	return wrote, kept, err
}

// wrapperDescription is the wrapper pack's one-line description, reusing the plugin's own when
// it has one so `pack ls` says something useful without the author writing it twice.
func wrapperDescription(pluginName, pluginDesc string) string {
	if pluginDesc != "" {
		return pluginDesc + " (agent plugin " + pluginName + ", wrapped as a pack)"
	}
	return "Agent plugin " + pluginName + ", wrapped as a yolo pack"
}

// pluginWrapperReadme documents the two decisions init could not make for the author: which
// tool's skills dir this delivers to, and whether the plugin travels with the pack.
func pluginWrapperReadme(name, pluginName, packRoot, pluginAbs string) string {
	return "# " + name + "\n\n" +
		"A yolo-jail pack wrapping the agent plugin `" + pluginName + "`.\n\n" +
		"The plugin tree is delivered VERBATIM — manifest included — to a skills dir whose\n" +
		"tool loads plugin manifests, so its skills invoke as `/<namespace>-" + packname.Normalize(pluginName) + ":<skill>`\n" +
		"and cannot collide with your own. Nothing beside it in that dir is touched.\n\n" +
		"Consume it by adding to `packs` in `~/.config/yolo-jail/config.toml`\n" +
		"(`yolo pack add` takes a shipped name or `source/pack`, not a `file://` address):\n\n" +
		"```toml\npacks = [\"file://" + packRoot + "\"]\n```\n\n" +
		"## Two things to check\n\n" +
		"1. **The destination.** `pack.toml` targets `.claude/skills`. Change `into` if you\n" +
		"   want a different tool's dir — the plugin is delivered whole, and only a\n" +
		"   tool that reads `.claude-plugin/plugin.json` loads what it declares.\n" +
		"2. **It is a COPY.** `skills/" + pluginName + "` was copied from\n" +
		"   `" + pluginAbs + "`, not linked (a pack may not contain a symlink pointing\n" +
		"   outside itself). Re-run `yolo pack init --from-plugin` here to refresh it after\n" +
		"   updating the plugin; existing files are reported and left alone, so delete the\n" +
		"   subtree first for a clean refresh.\n\n" +
		"Validate with `yolo pack lint`, and see what it claims with `yolo pack footprint .`.\n"
}
