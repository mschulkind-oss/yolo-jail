package macosuser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// THE HOST'S RULES AND THE BOOTSTRAP'S DELIVERY AGREE, whatever an earlier session left in the
// workspace.
//
// ResolveHomeReadonly runs on the host, before the bootstrap, and joins each destination onto the
// resolved workspace and account home as TEXT. That text is the path the kernel reports only if
// the bootstrap lays nothing through a symbolic link it did not lay itself — and the sidecar is in
// the workspace, which every session can write. So this drives the two REAL bootstrap steps
// (entrypoint.InstallDarwinHomeLayout, entrypoint.InstallHomeOverlay) over a planted link at each
// position, then asks the filesystem where the delivered bytes physically are:
//
//   - every file carrying this launch's content must sit under a path the profile denies, and every
//     directory between it and the workspace must be an anchor — whether the launch then succeeds
//     or refuses;
//   - a launch whose content steps succeeded must have delivered every file, so the first check is
//     not passed by delivering nothing.
//
// This is the review's reproduction (a codex launch over `<ws>/.yolo/home/codex` planted as a link,
// and over a planted `skills` link) turned into a regression test.
func TestTheBootstrapDeliversOnlyWhereTheHostsRulesPoint(t *testing.T) {
	all, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	var packs []*packload.Pack
	for _, p := range all {
		if p.Name == "codex" {
			packs = append(packs, p)
		}
	}
	if len(packs) != 1 {
		t.Fatalf("found %d codex packs among the shipped set", len(packs))
	}
	dests := []string{".codex/skills", ".codex/AGENTS.md"}

	// Each plant runs between a clean first launch and the launch under test, which is when an
	// earlier session's agent would have made it.
	plants := map[string]func(t *testing.T, ws, sidecar string){
		"nothing planted": nil,
		"a link at the agent's sidecar dir": func(t *testing.T, ws, sidecar string) {
			replaceWithLink(t, filepath.Join(sidecar, "codex"), filepath.Join(ws, "evil"))
		},
		"a link at the skills dir": func(t *testing.T, ws, sidecar string) {
			writeFile(t, filepath.Join(ws, "evil", "planted", "SKILL.md"), "the agent's own skill")
			replaceWithLink(t, filepath.Join(sidecar, "codex", "skills"), filepath.Join(ws, "evil"))
		},
		"a link at the briefing": func(t *testing.T, ws, sidecar string) {
			writeFile(t, filepath.Join(ws, "evil.md"), "the agent's own file")
			replaceWithLink(t, filepath.Join(sidecar, "codex", "AGENTS.md"), filepath.Join(ws, "evil.md"))
		},
		"a link at the sidecar": func(t *testing.T, ws, sidecar string) {
			replaceWithLink(t, sidecar, filepath.Join(ws, "evil"))
		},
		"a link at .yolo": func(t *testing.T, ws, sidecar string) {
			replaceWithLink(t, filepath.Dir(sidecar), filepath.Join(ws, "evil"))
		},
	}

	for name, plant := range plants {
		t.Run(name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			home, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
			sidecar := filepath.Join(ws, ".yolo", "home")
			for _, d := range []string{home, ws} {
				if err := os.MkdirAll(d, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := bootContent(t, home, sidecar, packs, "first launch"); err != nil {
				t.Fatalf("the clean first launch failed: %v", err)
			}
			if plant != nil {
				plant(t, ws, sidecar)
			}
			const marker = "this launch's content"
			bootErr := bootContent(t, home, sidecar, packs, marker)

			rules := ResolveHomeReadonly(home, ws, packload.WritableDirs(packs), dests)

			var found []string
			_ = filepath.Walk(base, func(p string, fi os.FileInfo, err error) error {
				if err != nil || !fi.Mode().IsRegular() {
					return nil
				}
				if b, rerr := os.ReadFile(p); rerr == nil && string(b) == marker {
					found = append(found, p)
				}
				return nil
			})
			for _, p := range found {
				if !underAny(rules.Paths, p) {
					t.Errorf("%s holds this launch's content and no content rule covers it — the "+
						"agent can edit it.\nPaths: %v\nbootstrap: %v", p, rules.Paths, bootErr)
					continue
				}
				for dir := filepath.Dir(p); dir != ws && strings.HasPrefix(dir, ws+"/"); dir = filepath.Dir(dir) {
					if !underAny(rules.Paths, dir) && !hasEntry(rules.Anchors, dir) {
						t.Errorf("%s is on the physical chain to %s and is not an anchor, so it "+
							"can be moved aside.\nAnchors: %v", dir, p, rules.Anchors)
					}
				}
			}
			if bootErr == nil {
				for _, rel := range []string{".codex/skills/demo/SKILL.md", ".codex/AGENTS.md"} {
					if b, err := os.ReadFile(filepath.Join(home, rel)); err != nil || string(b) != marker {
						t.Errorf("the content steps succeeded and ~/%s reads %q (err %v), want "+
							"this launch's content", rel, b, err)
					}
				}
			}
		})
	}
}

// bootContent runs the bootstrap's two content steps, layout then overlay, the way
// RunDarwinBootstrap does: both run whatever the first returns. body is the content delivered.
func bootContent(t *testing.T, home, sidecar string, packs []*packload.Pack, body string) error {
	t.Helper()
	tree := t.TempDir()
	writeFile(t, filepath.Join(tree, ".codex", "skills", "demo", "SKILL.md"), body)
	writeFile(t, filepath.Join(tree, ".codex", "AGENTS.md"), body)
	e := entrypoint.DarwinEnvFrom(map[string]string{
		"HOME":                          home,
		entrypoint.DarwinHomeSidecarEnv: sidecar,
		"YOLO_DARWIN_HOME_OVERLAY":      tree,
	}, home)
	layoutErr := entrypoint.InstallDarwinHomeLayout(e, packs)
	if err := entrypoint.InstallHomeOverlay(e, packs); err != nil {
		return err
	}
	return layoutErr
}

// replaceWithLink replaces whatever is at path with a symbolic link to target, creating target.
func replaceWithLink(t *testing.T, path, target string) {
	t.Helper()
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// underAny reports whether p is one of roots or below one — a `(subpath …)` match.
func underAny(roots []string, p string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}
