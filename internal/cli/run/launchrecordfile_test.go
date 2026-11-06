package run

import (
	"go/ast"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// TestTheLaunchRecordCarriesTheLaunchsSetupAndPacks is slice 11b's call-site pin (named-setups.md
// SU-D11): the record runContainer writes under the launch guard's hold is the one containerLaunchRecord
// builds from THIS launch, its cname and the packs it staged, so the record names the launch's
// setup, confinement and packs. The AST half fails if the call site writes any other record (a
// workspace alone, as before 11b) or stops writing one; the unit half fails if containerLaunchRecord drops
// a field.
func TestTheLaunchRecordCarriesTheLaunchsSetupAndPacks(t *testing.T) {
	ws := t.TempDir()
	at := time.Date(2026, 11, 6, 14, 2, 11, 0, time.UTC)
	o := &Options{Workspace: ws, Setup: setup.MustBuiltIn("podman-self"), Now: func() time.Time { return at }}
	cname := o.jailName()
	wantVersion := version.Binary("") // an unstamped binary that cannot describe itself is unknown
	if wantVersion == "unknown" {
		wantVersion = ""
	}
	rec := o.containerLaunchRecord(cname, []*packload.Pack{{Name: "claude"}, nil, {Name: "codex"}})
	if rec.Name != cname || rec.Setup != "podman-self" || rec.Confinement != setup.ConfinementPodman ||
		rec.RunAs != setup.RunAsSelf || rec.Workspace != resolvePath(ws) || !rec.Started.Equal(at) ||
		!slices.Equal(rec.Packs, []string{"claude", "codex"}) || rec.YoloVersion != wantVersion {
		t.Errorf("containerLaunchRecord = %+v; want the launch's name, setup, confinement, run_as, workspace, start, packs and version", rec)
	}
	if none := o.containerLaunchRecord(cname, nil); none.Packs == nil {
		t.Error("a launch that staged no pack must record an empty pack list, not an unknown one")
	}

	t.Setenv("HOME", t.TempDir())
	if err := runtimeWriteTracking(rec); err != nil {
		t.Fatal(err)
	}
	if got, ok := yoloruntime.ReadLaunchRecord(cname); !ok || got.Setup != "podman-self" || !slices.Equal(got.Packs, rec.Packs) {
		t.Errorf("runtimeWriteTracking wrote %+v, %v; want the launch's record", got, ok)
	}

	var args []ast.Expr
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && skelCallee(call) == "runtimeWriteTracking" {
			args = call.Args
			return false
		}
		return true
	})
	if len(args) != 1 {
		t.Fatalf("runContainer no longer writes its launch record through runtimeWriteTracking(rec); re-anchor this pin, do not delete it")
	}
	built, ok := args[0].(*ast.CallExpr)
	if !ok || skelCallee(built) != "containerLaunchRecord" || len(built.Args) != 2 ||
		skelIdent(built.Args[0]) != "cname" || skelIdent(built.Args[1]) != "loadedPacks" {
		t.Errorf("runContainer's launch record is not o.containerLaunchRecord(cname, loadedPacks): the record must carry this launch's setup and staged packs")
	}
}
