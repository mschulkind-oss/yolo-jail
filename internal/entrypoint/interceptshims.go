package entrypoint

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// interceptshims.go writes the `intercept` contributions of the selected packs into the
// block dir (packdecl.KindIntercept; docs/design/boundary-broker.md OQ-BB8): a shim at
// ~/.yolo/bin/block/<bin> that execs the pack's forwarder with the caller's arguments.
//
// THE BLOCK DIR IS WHERE INTERCEPTION LIVES. It is first on PATH, ahead of the launchers
// and every install prefix, which is exactly the position a forwarder needs: the image
// bakes some tools at /bin (gh among them), and launchercollision.go writes no launcher for
// a name the image provides, so no other generated directory could outrank one. This is
// also why no launcher-collision rule applies here: the point is to stand in front of the
// installed program, which stays at its own path for anyone who names it.
//
// Core names no tool: the shim is a name and an argv prefix from a manifest.

// GenerateIntercepts writes one forwarding shim per intercept contribution. It runs after
// the blockers (GenerateShims), so a blocker for the same name — a pack's or the user's own
// `security.blocked_tools` — is already on disk and WINS: a refusal is the more specific
// statement, and the boot says which one it kept.
func GenerateIntercepts(e *Env) error {
	packs, err := LoadJailPacks(e)
	if err != nil || len(packs) == 0 {
		// LoadJailPacks' failure is reported by the steps that render from the packs; a
		// jail with no pack tree has nothing to intercept.
		return nil
	}
	// The PATH the agent will have, without the block dir itself: what `<bin>` resolved to
	// before the intercept, which is where YOLO_BYPASS_SHIMS=1 sends it.
	behind := pathListWithout(agentPath(e), e.BlockDir())
	seen := map[string]string{}
	for _, ic := range packload.Intercepts(packs) {
		if !packdecl.ValidBinName(ic.Name) || len(ic.Forward) == 0 || !packdecl.ValidBinName(ic.Forward[0]) ||
			ic.Forward[0] == ic.Name {
			// The schema refuses each of these; this is the writer-side half, since the shim
			// is FILED at filepath.Join(BlockDir, name).
			continue
		}
		if owner, dup := seen[ic.Name]; dup {
			e.warn("pack " + ic.Pack + ": not intercepting " + ic.Name + " — pack " + owner +
				" already does, and one name has one forwarder")
			continue
		}
		seen[ic.Name] = ic.Pack
		shimPath := filepath.Join(e.BlockDir(), ic.Name)
		if pathExists(shimPath) {
			e.warn("pack " + ic.Pack + ": not intercepting " + ic.Name + " — a blocked-tool entry " +
				"for it wins, since a refusal is the more specific statement")
			continue
		}
		content := InterceptShimContent(ic.Pack, ic.Name, lookPathIn(behind, ic.Name), ic.Forward)
		if err := writeExecutable(shimPath, content); err != nil {
			return err
		}
	}
	return nil
}

// InterceptShimContent renders one forwarding shim. Every word is shquote'd into a bare
// argv word, since the manifest is a pack's and the values reach a shell.
//
// realBin is where `<bin>` resolved behind the block dir, "" when nothing did: with
// YOLO_BYPASS_SHIMS set the shim execs it, which is how an installer or a script that
// needs the real tool reaches it, as with every other shim here.
//
// THE HEADER SAYS IT IS NOT A BLOCKER (docs/design/boundary-broker.md BB-D67). The shim lives
// in the block dir by OQ-BB8's ruling, so `type <bin>` names ~/.yolo/bin/block/<bin>, which
// reads as a refusal to whoever is debugging; the first lines of the file say what it is
// instead: whose forwarder, what it runs, and what is behind it.
func InterceptShimContent(pack, bin, realBin string, forward []string) string {
	b := sanitizeComment(bin)
	behind := "# Behind it: " + sanitizeComment(realBin) + ", which YOLO_BYPASS_SHIMS=1 runs instead."
	if realBin == "" {
		behind = "# Behind it: no " + b + " is installed, so YOLO_BYPASS_SHIMS=1 has nothing to run."
	}
	lines := []string{
		"#!/bin/sh",
		"# NOT A BLOCKER: this " + b + " is pack " + sanitizeComment(pack) + "'s forwarder (a yolo intercept).",
		"# It runs `" + sanitizeComment(strings.Join(forward, " ")) + "` with the same arguments.",
		"# Its directory comes first on PATH, so it holds intercepts as well as blocked tools.",
		behind,
		`if [ -n "$YOLO_BYPASS_SHIMS" ]; then`,
	}
	if realBin != "" {
		lines = append(lines, "  exec "+shquote.Quote(realBin)+` "$@"`)
	} else {
		lines = append(lines,
			echoStderr("  ", "YOLO_BYPASS_SHIMS is set, and no "+bin+" is installed behind this intercept"),
			"  exit 127")
	}
	lines = append(lines, "fi", "exec "+shquote.Join(forward)+` "$@"`, "")
	return strings.Join(lines, "\n")
}

// sanitizeComment keeps a value on one comment line.
func sanitizeComment(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

// pathListWithout drops one directory from a PATH list.
func pathListWithout(path, dir string) string {
	var keep []string
	for _, d := range filepath.SplitList(path) {
		if filepath.Clean(d) != filepath.Clean(dir) {
			keep = append(keep, d)
		}
	}
	return strings.Join(keep, string(os.PathListSeparator))
}
