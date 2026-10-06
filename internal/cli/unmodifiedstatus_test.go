package cli

// unmodifiedstatus_test.go pins `yolo pack status` for an UNMODIFIED EXTENSION held at a revision
// that never moves — a git tag or full commit, or an exact npm version — which a check resolves
// once and no launch checks again (docs/design/pi-extension-store-builds.md XB-D37): the status
// must not promise a next check no launch runs, and must speak of no series it does not have.

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusOfAFixedUnmodifiedExtensionPromisesNoCheck(t *testing.T) {
	for _, kind := range []string{"commit", "tag", "npm version"} {
		t.Run(kind, func(t *testing.T) {
			var fx *treeFixture
			switch kind {
			case "npm version":
				fx = newNpmTreeFixture(t, "@1.2.0").treeFixture
			default:
				fx = newTreeFixture(t, "")
				tip := fx.commit(t, "v2.0.0", map[int]string{3: "three"})
				ref := tip
				if kind == "tag" {
					ref = "v2.0.0"
				}
				writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
					`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=`+ref+`"}]}`)
			}
			if d, out := fx.deliver(t, true); d.Dir == "" {
				t.Fatalf("no build: %s", out)
			}
			fx.now = fx.now.Add(3 * time.Hour)
			if d, out := fx.deliver(t, true); d.Dir == "" || strings.Contains(out, "checking extension") {
				t.Fatalf("a fixed %s was checked again, or no build: %s", kind, out)
			}
			lines := strings.Join(patchedForkStatusLines(fx.tree(t)), "\n")
			if strings.Contains(lines, "next check: in") || strings.Contains(lines, "next check: due") {
				t.Errorf("the status promises a check no launch runs:\n%s", lines)
			}
			if !strings.Contains(lines, "no launch checks it again") || !strings.Contains(lines, "`yolo pack update` checks now") {
				t.Errorf("the status does not say no launch checks it again, or how to check now:\n%s", lines)
			}
			if !strings.HasPrefix(lines, treeKeyCLI) || !strings.Contains(lines, "unmodified extension at ~/.tool/ext/tool-ext") {
				t.Errorf("the status's head does not name an unmodified extension at its landing:\n%s", lines)
			}
			if strings.Contains(lines, "series") {
				t.Errorf("an unmodified extension's status speaks of a series:\n%s", lines)
			}
		})
	}
}
