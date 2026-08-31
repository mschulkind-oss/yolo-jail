package version

import (
	"context"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// ImageSourcePaths are the repo-relative paths whose CONTENT decides what the jail
// image contains: flake.nix's `goSrc` fileset (go.mod, go.sum, vendor/, cmd/,
// internal/, packs/) plus the two flake files, which pick the packages, the Env and
// the install prefix.
//
// It is the same set `nix eval .#installPrefix.outPath` covers — the oracle the
// integration harness already uses to refuse a stale image
// (integration/harness_test.go, ensureJailImage). This list is the host-side twin of
// that oracle, for the one comparison the harness cannot make: the harness always
// builds a fresh CLI, so it can never observe a host binary older than the tree.
//
// TestImageSourcePathsMatchTheFlake pins it against flake.nix's own fileset, because
// a path that drifts out of this list makes SourceSkew silent about exactly the
// change it exists to catch.
var ImageSourcePaths = []string{
	"cmd",
	"flake.lock",
	"flake.nix",
	"go.mod",
	"go.sum",
	"internal",
	"packs",
	"vendor",
}

// Skew is a source tree that has moved past the binary reading it, in a way that
// changes what the jail image would contain. See SourceSkew.
type Skew struct {
	// BinaryCommit is the commit this binary was stamped with at build time
	// (GitCommit), resolved to a full sha in the tree.
	BinaryCommit string
	// TreeCommit is the tree's current HEAD.
	TreeCommit string
	// Changed are the ImageSourcePaths entries that differ between the two,
	// sorted. Never empty in a returned Skew.
	Changed []string
}

// SourceSkew reports whether the source tree at repoRoot would build a jail image
// from source this binary was NOT built from, or nil when it would not.
//
// # Why this exists
//
// The two halves of yolo deploy on INDEPENDENT CADENCES, and only one of them is
// automatic. The image is rebuilt from the tree on every launch (AutoLoadImage
// notices the nix store path changed and reloads); the host `yolo` binary changes
// only when a human runs `just install`. So every commit that moves a host↔jail
// contract — a mount destination, an env var name, an argv the entrypoint parses —
// leaves the machine in a skewed state BY DEFAULT, and stays there silently until
// someone remembers to reinstall.
//
// What that looks like when it bites: `just install`ed at 0bd68262, then committed
// a813b865 ("gather the jail's generated script dirs as ~/.yolo/bin/{block,launch}")
// and launched. The old launcher mounted the retired ~/.yolo-shims and
// ~/.yolo-launchers anchors and its storage.EnsureGlobalStorage never created
// ~/.local/share/yolo-jail/home/.yolo; the newly-built entrypoint then ran
// resetAnchorDir(~/.yolo/bin/block) against a read-only /home/agent and the boot
// refused with `mkdir /home/agent/.yolo: read-only file system` — three genSteps
// deep, naming neither half of the real cause, after streaming a 3.3 GB image.
// That commit's own message predicted the failure and said "deploy both"; a
// sentence in a commit message is not a mechanism.
//
// # What it compares, and what it deliberately ignores
//
// The binary's stamped commit (GitCommit, set by `just install` and
// scripts/build-go.sh) against the tree's HEAD, but ONLY through ImageSourcePaths.
// A docs-only commit moves HEAD and changes no image, so it must stay silent or the
// refusal becomes noise in a repo whose commits are mostly docs. Uncommitted work is
// invisible for the same reason it is invisible to nix: HEAD has not moved, both
// halves would build from the same commit, and the by-path verification loop
// (`just build-go && ./dist-go/linux-$(go env GOARCH)/yolo -- bash`) stamps the
// fresh binary with that same HEAD.
//
// # It returns nil rather than guessing
//
// No repoRoot, no stamp (an unstamped `go build`, or the baked in-jail binary, which
// flake.nix does not stamp), a stamp naming a commit this repo does not have (a
// binary from another checkout), or git unavailable: all nil. A skew this cannot
// PROVE is not one it reports — the same disposition rule the reachability witness
// follows, where a host yolo could not ask is never refused for what it cannot help.
func SourceSkew(repoRoot string) *Skew {
	if repoRoot == "" || GitCommit == "" || GitCommit == "unknown" {
		return nil
	}

	binary, ok := gitOut(repoRoot, "rev-parse", "--verify", "--quiet", GitCommit+"^{commit}")
	if !ok || binary == "" {
		return nil // the stamp names no commit in THIS repo
	}
	head, ok := gitOut(repoRoot, "rev-parse", "--verify", "HEAD")
	if !ok || head == "" || head == binary {
		return nil
	}

	args := append([]string{"diff", "--name-only", binary, head, "--"}, ImageSourcePaths...)
	out, ok := gitOut(repoRoot, args...)
	if !ok || out == "" {
		return nil // HEAD moved, but not through anything the image is built from
	}
	return &Skew{BinaryCommit: binary, TreeCommit: head, Changed: topLevel(out)}
}

// topLevel folds `git diff --name-only` output back to the ImageSourcePaths entries
// it fell under, so the report names "internal, packs" rather than 200 files.
func topLevel(nameOnly string) []string {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(nameOnly), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		root := line
		if i := strings.Index(line, "/"); i >= 0 {
			root = line[:i]
		}
		seen[path.Clean(root)] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gitOut runs git in repoRoot and returns its trimmed stdout. ok is false for any
// failure — a missing git, a non-repo, an unknown rev — because every caller here
// treats "cannot tell" and "nothing to report" identically.
func gitOut(repoRoot string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
