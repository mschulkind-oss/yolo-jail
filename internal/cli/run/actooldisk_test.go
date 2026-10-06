package run

// actooldisk_test.go pins OQ-MB1 (docs/research/macos-backend-performance.md, ruled A on
// 2026-10-05) at the launch: on Apple Container each workspace's /mise is a tool disk of its
// own — a named volume, so an ext4 disk image and case-sensitive — which the launch creates,
// labelled with its workspace, before `container run` names it. Every Apple Container jail
// mounted one shared volume before, and while one ran, a jail in another workspace failed at
// once with VZErrorDomain Code=2.
//
// ⚠ UNMEASURED ON HARDWARE. These pin the argv and the runtime calls against the Exec
// stand-in; the Mac that answers whether two jails now start together is the Apple Container
// parity job, through TestAppleContainerKeeperSweepSparesAKeptJail.

import (
	"bytes"
	"go/ast"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// miseMountSource is the source of the argv's `-v <source>:/mise`, or "".
func miseMountSource(argv []string) string {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-v" {
			if src, ok := strings.CutSuffix(argv[i+1], ":/mise"); ok {
				return src
			}
		}
	}
	return ""
}

func TestAppleContainerMountsItsWorkspacesOwnToolDisk(t *testing.T) {
	ws, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	o, in, _ := acPackInput(t, ws, home)

	sources := map[string]string{}
	for _, cname := range []string{runtime.FromResolved("/Users/m/app"), runtime.FromResolved("/Users/m/other")} {
		in.cname = cname
		src := miseMountSource(o.assembleRunCmd(in))
		if src != prune.MiseVolumeName(cname) {
			t.Fatalf("an Apple Container jail named %s mounts %q at /mise, want its own tool disk %q",
				cname, src, prune.MiseVolumeName(cname))
		}
		sources[cname] = src
	}
	if len(sources) != 2 || sources[runtime.FromResolved("/Users/m/app")] == sources[runtime.FromResolved("/Users/m/other")] {
		t.Fatalf("two workspaces mount one tool disk (%v): VZ attaches a disk to one VM at a time", sources)
	}
	for _, src := range sources {
		// A source with a slash is a host folder over virtiofs, on APFS: case-insensitive by
		// default, where a Linux tool tree's case pairs become one file. A bare name is a
		// named volume, an ext4 disk.
		if strings.Contains(src, "/") || src == prune.SharedMiseVolume {
			t.Errorf("/mise is mounted from %q, which is not this workspace's ext4 tool disk", src)
		}
	}

	// A sealed build keeps its own folder, as before (seal.go).
	in.sealed, in.miseStore = true, filepath.Join(ws, ".yolo", sealedMiseLeaf)
	if src := miseMountSource(o.assembleRunCmd(in)); src != in.miseStore {
		t.Errorf("a sealed Apple Container build mounts %q at /mise, want its own %q", src, in.miseStore)
	}
}

// Podman's machine-wide volume is untouched (the ruling's own line): a Podman Machine volume
// attaches to any number of containers, so every podman jail on a Mac keeps the shared store.
func TestPodmanOnAMacKeepsTheSharedMiseVolume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.IsLinux, o.IsMacOS = false, true
	in := relocationInput(t, "podman", "/ws/.yolo/home", nil)
	if src := miseMountSource(o.assembleRunCmd(in)); src != prune.SharedMiseVolume {
		t.Errorf("a podman jail on a Mac mounts %q at /mise, want the shared %q", src, prune.SharedMiseVolume)
	}
}

// toolDiskExec is the Exec stand-in: `container volume inspect` answers from `exists`, and
// `container volume create` succeeds unless createFails, adding the disk.
type toolDiskExec struct {
	exists      map[string]bool
	createFails bool
	// raced makes a failing create's disk appear anyway: another launch made it meanwhile.
	raced bool
	calls [][]string
}

func (f *toolDiskExec) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	f.calls = append(f.calls, argv)
	switch {
	case len(argv) == 4 && slices.Equal(argv[:3], []string{"container", "volume", "inspect"}):
		if f.exists[argv[3]] {
			return ExecResult{Ran: true, Stdout: `[{"id":"` + argv[3] + `"}]`}
		}
		return ExecResult{Ran: true, RC: 1, Stderr: "Error: volume not found: " + argv[3]}
	case len(argv) > 3 && slices.Equal(argv[:3], []string{"container", "volume", "create"}):
		name := argv[len(argv)-1]
		if f.createFails {
			if f.raced {
				f.exists[name] = true
			}
			return ExecResult{Ran: true, RC: 1, Stderr: "Error: storage error: no space left on device\n"}
		}
		f.exists[name] = true
		return ExecResult{Ran: true, Stdout: name + "\n"}
	}
	return ExecResult{Ran: false}
}

func (f *toolDiskExec) creates() [][]string {
	var out [][]string
	for _, c := range f.calls {
		if len(c) > 2 && c[1] == "volume" && c[2] == "create" {
			out = append(out, c)
		}
	}
	return out
}

func TestTheLaunchCreatesTheToolDiskLabelledWithItsWorkspace(t *testing.T) {
	ws := t.TempDir()
	cname := runtime.FromWorkspace(ws)
	name := prune.MiseVolumeName(cname)

	cases := []struct {
		name               string
		fake               *toolDiskExec
		wantCreate         bool
		wantSay, wantNoSay string
	}{
		{"a new workspace gets its disk, created and said once",
			&toolDiskExec{exists: map[string]bool{}}, true,
			"Created this workspace's tool disk " + name, "Warning"},
		{"a workspace that has one is left alone",
			&toolDiskExec{exists: map[string]bool{name: true}}, false,
			"", "tool disk"},
		{"a create that lost a race to another launch is not a failure",
			&toolDiskExec{exists: map[string]bool{}, createFails: true, raced: true}, true,
			"", "Warning"},
		{"a create that failed is warned, with what it costs and how to undo it",
			&toolDiskExec{exists: map[string]bool{}, createFails: true}, true,
			"`container volume rm " + name + "`", "Created"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			o := &Options{Workspace: ws, Exec: tc.fake.exec, Stdout: &out}
			o.ensureAppleContainerToolDisk(cname, o.pr(&out))

			creates := tc.fake.creates()
			if (len(creates) > 0) != tc.wantCreate {
				t.Fatalf("create calls %v, want a create: %v", creates, tc.wantCreate)
			}
			if tc.wantCreate {
				want := prune.MiseVolumeCreateArgv(cname, runtime.ResolveWorkspace(ws))
				if !slices.Equal(creates[0], want) {
					t.Errorf("created with %q, want %q", creates[0], want)
				}
				// The label is the reaper's evidence: it must name this workspace's container.
				label := strings.TrimPrefix(creates[0][6], prune.MiseVolumeWorkspaceLabel+"=")
				if runtime.FromResolved(label) != cname {
					t.Errorf("the disk's workspace label %q names container %s, not %s, so `yolo prune` "+
						"would never call its workspace gone", label, runtime.FromResolved(label), cname)
				}
			}
			if tc.wantSay != "" && !strings.Contains(out.String(), tc.wantSay) {
				t.Errorf("the launch did not say %q:\n%s", tc.wantSay, out.String())
			}
			if tc.wantNoSay != "" && strings.Contains(out.String(), tc.wantNoSay) {
				t.Errorf("the launch said %q:\n%s", tc.wantNoSay, out.String())
			}
		})
	}
}

// THE CALL SITE. runContainer must create the disk on Apple Container's fresh path, after the
// last attach decision (an attach mounts nothing) and before the argv naming it is assembled,
// for the workspace's own container name, and never under the seal, whose /mise is a folder of
// its own. Delete the call, move it, or drop the gate, and this fails.
func TestRunContainerCreatesTheToolDiskBeforeTheArgvNamesIt(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var lastAttach, assemble token.Pos
	var ensure *ast.CallExpr
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "attachExisting":
			if call.Pos() > lastAttach {
				lastAttach = call.Pos()
			}
		case "assembleRunCmd":
			assemble = call.Pos()
		case "ensureAppleContainerToolDisk":
			if ensure != nil {
				t.Error("runContainer creates the tool disk twice")
			}
			ensure = call
		}
		return true
	})
	if ensure == nil {
		t.Fatal("runContainer never calls ensureAppleContainerToolDisk: `container run` makes the disk " +
			"itself, unlabelled, and `yolo prune` can then never tell when its workspace is gone")
	}
	if lastAttach == token.NoPos || assemble == token.NoPos {
		t.Fatal("this pin's anchors (attachExisting, assembleRunCmd) moved; re-anchor it, do not delete it")
	}
	if !(lastAttach < ensure.Pos() && ensure.Pos() < assemble) {
		t.Error("the tool disk is created outside the window between the last attach decision and the argv")
	}
	if len(ensure.Args) == 0 || skelIdent(ensure.Args[0]) != "cname" {
		t.Error("the tool disk is created for something other than this launch's cname, the name the argv mounts")
	}

	// Gated on Apple Container and on the seal, by the if statement that holds it.
	var gate *ast.IfStmt
	ast.Inspect(fd, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok && ifs.Body.Pos() <= ensure.Pos() && ensure.End() <= ifs.Body.End() {
			gate = ifs
		}
		return true
	})
	if gate == nil {
		t.Fatal("the tool disk is created on every backend; only Apple Container mounts one")
	}
	cond := exprString(gate.Cond)
	if !strings.Contains(cond, `rt == "container"`) || !strings.Contains(cond, "!o.Sealed") {
		t.Errorf("the create is gated by %q; want Apple Container and not sealed", cond)
	}
}

// exprString renders the small boolean expressions the call-site pins read.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		return exprString(x.X) + " " + x.Op.String() + " " + exprString(x.Y)
	case *ast.UnaryExpr:
		return x.Op.String() + exprString(x.X)
	case *ast.ParenExpr:
		return "(" + exprString(x.X) + ")"
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		return x.Value
	}
	return "?"
}
