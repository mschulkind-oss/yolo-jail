package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// The native home layout is a directory binding, even on a Linux synthetic home.
// Exercise the generated launcher and the real internal materializer, not a verifier stub.
func TestForkLauncherMaterializesThroughDarwinDirectoryBindings(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	for _, output := range []string{".local/bin/tool", ".local/bin", ".local/share"} {
		t.Run(output, func(t *testing.T) {
			home, sidecar, workspace := t.TempDir(), t.TempDir(), t.TempDir()
			layout := entrypoint.DeriveDarwinHomeLayout(home, sidecar, nil, nil)
			if err := layout.Apply(); err != nil {
				t.Fatal(err)
			}
			localTarget, err := os.Readlink(filepath.Join(home, ".local"))
			if err != nil || localTarget != filepath.Join(sidecar, "local") {
				t.Fatalf("native local binding: %q, %v", localTarget, err)
			}
			// Also exercise a directory that IS declared output, rather than only its ancestors.
			boundDirectory := ""
			if output != ".local/bin/tool" {
				boundDirectory = filepath.Join(home, ".local/bin")
				if output == ".local/share" {
					boundDirectory = filepath.Join(home, ".local/share/vendor")
				}
				bound := filepath.Join(sidecar, "bound-output")
				if err := os.Mkdir(bound, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(boundDirectory), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(bound, boundDirectory); err != nil {
					t.Fatal(err)
				}
			}
			store := &capture.Store{Dir: t.TempDir()}
			body := "#!/bin/sh\nprintf 'admitted-fork\\n'\n"
			staged, err := store.Stage("directory-bindings")
			if err != nil {
				t.Fatal(err)
			}
			tree := capture.TreeDir(staged)
			targetRel := ".local/share/vendor/tool"
			writeFile(t, filepath.Join(tree, targetRel), body)
			if err := os.Chmod(filepath.Join(tree, targetRel), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(tree, ".local/bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(home, targetRel)
			if err := os.Symlink(target, filepath.Join(tree, ".local/bin/tool")); err != nil {
				t.Fatal(err)
			}
			if err := capture.WriteManifest(staged, &capture.Manifest{Schema: capture.ManifestSchema, Home: home, Platform: capture.Platform(),
				Entries: []capture.ManifestEntry{
					{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
					{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
					{Path: ".local/bin/tool", Kind: capture.KindSymlink, Target: target},
					{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
					{Path: ".local/share/vendor", Kind: capture.KindDir, Mode: "0755"},
					{Path: targetRel, Kind: capture.KindFile, Mode: "0755", Size: int64(len(body))},
				}}); err != nil {
				t.Fatal(err)
			}
			entry, err := store.AdmitEntry(staged)
			if err != nil {
				t.Fatal(err)
			}
			if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), entrypoint.BuildReceipt{Bin: "tool", Source: forkTestSource,
				Key: entry.Key, Path: entry.Root, Platform: capture.Platform(), Revision: forkTestCommit,
				Act: entrypoint.ReceiptActRecord, Time: time.Now()}.Line()); err != nil {
				t.Fatal(err)
			}
			if _, err := capture.Materialize(capture.MaterializeOptions{Entry: entry, Home: home}); err != nil {
				t.Fatal(err)
			}
			packs := t.TempDir()
			writeFile(t, filepath.Join(packs, "base/pack.json"), `{"name":"base","contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"}]}`)
			outputs := `".local/bin/tool"`
			if output != ".local/bin/tool" {
				outputs += `,"` + output + `"`
			}
			writeFile(t, filepath.Join(packs, "fork/pack.json"), `{"name":"fork","contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"base","source":"git+https://example.invalid/tool?ref=main","build":"make install","produces":[`+outputs+`]}]}`)
			e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_WORKSPACE": workspace, "YOLO_PACK_ROOT": packs,
				entrypoint.CapturesDirEnv: store.Dir, entrypoint.ForkBuildsEnv: entrypoint.ForkBuildsWire(map[string]entrypoint.ForkDelivery{"tool": {Key: entry.Key}})})
			e.Stderr = &bytes.Buffer{}
			t.Cleanup(packload.OverrideSkewTolerance(false))
			if err := entrypoint.GenerateAgentLaunchers(e); err != nil {
				t.Fatal(err)
			}
			stamp := filepath.Join(home, ".local/state/yolo/fork-keys/tool")
			writeFile(t, stamp, entry.Key+"\n")
			testBinary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			helperBin := t.TempDir()
			writeFile(t, filepath.Join(helperBin, "yolo"), "#!/bin/sh\nexport YOLO_CACHED_GOOD_MATERIALIZER_HELPER=1\nexec "+shquote.Quote(testBinary)+" -test.run=^TestCachedGoodMaterializerHelperProcess$ -- \"$@\"\n")
			if err := os.Chmod(filepath.Join(helperBin, "yolo"), 0o755); err != nil {
				t.Fatal(err)
			}
			launch := func(wantRepair bool) {
				t.Helper()
				cmd := exec.Command(filepath.Join(e.LaunchDir(), "tool"))
				cmd.Dir = workspace
				cmd.Env = testtmp.ChildEnv([]string{"HOME=" + home, "PATH=" + helperBin + ":" + os.Getenv("PATH")})
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				if err := cmd.Run(); err != nil {
					t.Fatalf("generated launcher: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				if stdout.String() != "admitted-fork\n" {
					t.Fatalf("ran wrong program: %q", stdout.String())
				}
				if strings.Contains(stderr.String(), "Materialized tool") != wantRepair {
					t.Fatalf("repair=%t, stderr=%q", wantRepair, stderr.String())
				}
			}
			launch(false)
			if boundDirectory != "" {
				info, err := os.Stat(boundDirectory)
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatalf("binding mode changed: %v, %v", info, err)
				}
			}
			// Unlink first: the fallback mechanism may have hardlinked the admitted source.
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, []byte("#!/bin/sh\necho stale\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			launch(true)
			launch(false)
			got, err := os.Readlink(filepath.Join(home, ".local"))
			if err != nil || got != localTarget {
				t.Fatalf("repair changed native binding: %q, %v", got, err)
			}
		})
	}
}
