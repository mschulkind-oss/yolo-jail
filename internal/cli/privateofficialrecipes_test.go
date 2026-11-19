package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestPrivateOfficialRecipesUseDeclaredSelectionColdFreshAndRejectUnknowns(t *testing.T) {
	for _, pack := range []string{"opencode", "omp", "chrome-devtools"} {
		t.Run(pack, func(t *testing.T) {
			f, req := privateOfficialProducer(t)
			for _, recipe := range privateOfficialRecipes {
				raw, err := privateOfficialRecipeTarball(recipe)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(f.root, recipe.archive()), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, paths.UserConfigPath(), `{"packs":["`+pack+`"],"agent_updates":false,"host_apply_on_launch":false}`)
			selected := selectConfiguredHostPacks()
			if problems := selected.problems(); len(problems) != 0 {
				t.Fatal(problems)
			}
			cfg, err := config.LoadConfig(req.Workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Env, req.Packs = ordinarySelectionEnvironment(cfg, selected.packs), selected.packs
			vector, err := selectedOrdinaryInputs(req.Env, req.Packs, entrypoint.ProgramInstallOnly, req.Platform)
			if pack == "omp" && req.Platform != "linux/amd64" {
				if err != nil || len(vector) != 0 {
					t.Fatalf("unsupported official OMP platform must select nothing: %+v %v", vector, err)
				}
				return
			}
			if err != nil || len(vector) != 1 {
				t.Fatalf("actual unchanged %s selection: %+v %v", pack, vector, err)
			}
			f.in = vector[0]
			for _, fresh := range []bool{false, true} {
				req.Fresh, req.Act = fresh, &run.ActInterrupt{}
				got, err := prepareOrdinaryForLaunch(req, io.Discard)
				if err != nil || got[f.in.Bin].Compatibility == "" {
					t.Fatalf("normal %s producer fresh=%v: %+v %v", pack, fresh, got, err)
				}
				record, err := readNPMCaptureRecord(&capture.Store{Dir: f.store.Dir}, f.in)
				if err != nil || record.Resolution == nil || record.Resolution.Input != f.in {
					t.Fatalf("normal admitted input changed: %+v %v", record, err)
				}
			}
			if f.resolves.Load() != 2 || f.installs.Load() != 1 {
				t.Fatalf("actual cold/fresh resolution/install counters %d/%d", f.resolves.Load(), f.installs.Load())
			}
			ctx, cancel := context.WithTimeout(context.Background(), testHangGuard(t))
			defer cancel()
			for _, change := range []string{"package", "version", "bin"} {
				input := f.in
				switch change {
				case "package":
					input.Package = "private-unknown-package"
				case "version":
					name, _, _ := npmCaptureSpec(input.Package)
					input.Package = name + "@999.0.0"
				case "bin":
					input.Bin = "private-unknown-bin"
				}
				before, err := os.ReadFile(filepath.Join(f.root, "private-official-events"))
				if err != nil {
					t.Fatal(err)
				}
				var output bytes.Buffer
				_, err = resolveNPMCapture(ctx, input, t.TempDir(), t.TempDir(), &output)
				if err == nil || (change != "bin" && !strings.Contains(output.String(), "refuses unknown")) {
					t.Fatalf("%s unknown %s accepted: %v %s", pack, change, err, &output)
				}
				after, _ := os.ReadFile(filepath.Join(f.root, "private-official-events"))
				if change != "bin" && !bytes.Equal(before, after) {
					t.Fatalf("unknown %s installed or ran a fallback: %q", change, after)
				}
				if strings.Count(string(after), "ci\n") != 1 {
					t.Fatalf("unknown %s installed or ran a fallback: %q", change, after)
				}
			}
		})
	}
}
