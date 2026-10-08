package run

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosctxdirentry_test.go pins what a DIRECTORY host_files entry keeps on its way from the host
// home to the macos-user sandbox home — its files' permission bits — and the two failures the
// copy must report rather than absorb. Each drives a real Run() on the macos-user arm, for
// macosctxtree_test.go's reason.

// stageCtxTreeAsRoot copies the composed tree the way macosuser.StageCtxCommands' privileged half
// does on a Mac — `cp -R`, which keeps each file's mode bits, then `chmod -R a+rX` — and returns
// the copy. So the bootstrap below reads what it reads there: every file world-readable, whatever
// the host file was.
func stageCtxTreeAsRoot(t *testing.T, tree string) string {
	t.Helper()
	staged := filepath.Join(t.TempDir(), "staged-ctx")
	err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(tree, p)
		if err != nil {
			return err
		}
		out := filepath.Join(staged, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(out, info.Mode().Perm()|0o555)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		mode := info.Mode().Perm() | 0o444
		if mode&0o111 != 0 {
			mode |= 0o111
		}
		if err := os.WriteFile(out, b, mode); err != nil {
			return err
		}
		return os.Chmod(out, mode)
	})
	if err != nil {
		t.Fatalf("staging the composed tree as root would: %v", err)
	}
	return staged
}

// umaskedPerm is perm as this process's umask leaves a file created with it, measured by creating
// one: what a boot copy that creates its files with the source's bits can be expected to leave.
func umaskedPerm(t *testing.T, perm os.FileMode) os.FileMode {
	t.Helper()
	probe := filepath.Join(t.TempDir(), "umask-probe")
	if err := os.WriteFile(probe, nil, perm); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(probe)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// A DIRECTORY ENTRY'S FILES KEEP THEIR PERMISSION BITS, from the host home to the laid sandbox
// home: a 0600 private key arrives 0600, as a container's boot copy leaves it (copyTree creates
// each file with the source's bits). The staged tree between the two is world-readable by
// necessity (the bootstrap reads it as the sandbox account), so the bits cannot ride the staged
// files' own modes: ~/.ssh/id_ed25519 arrived 0644, and OpenSSH refuses a key that open. Through
// Run(), the plan's wire, the staging's `a+rX` and the REAL macos-user bootstrap on a laid home,
// so deleting either half of the carry — the host side recording the bits, or the bootstrap
// creating the file with them — fails here.
func TestMacosUserDirectoryHostFileKeepsEachFilesPermissions(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".ssh/", "source": "~/.ssh/"}]`)
	src := filepath.Join(home, ".ssh")
	files := map[string]os.FileMode{
		"id_ed25519":     0o600,
		"id_ed25519.pub": 0o644,
		"config":         0o640,
		"bin/connect.sh": 0o700,
		"bin/shared.sh":  0o755,
	}
	for rel, mode := range files {
		writeHostFileAt(t, filepath.Join(src, filepath.FromSlash(rel)), rel+"\n", mode)
		if err := os.Chmod(filepath.Join(src, filepath.FromSlash(rel)), mode); err != nil {
			t.Fatal(err)
		}
	}

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)
	if ctx.Tree == "" || len(ctx.HostFiles) != 1 {
		t.Fatalf("the launch composed no context tree for the directory entry: %+v", ctx)
	}
	// The host-side copy, in a staging dir under the user's yolo state dir, is no wider either.
	hostSide := filepath.Join(ctx.Tree, "host-user", ctx.HostFiles[0].Slug(), "id_ed25519")
	if fi, err := os.Stat(hostSide); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("the host-side copy of a 0600 key is %v (%v): any account that reaches the staging "+
			"dir can read it", fi, err)
	}

	plan := macosuser.BuildRunPlan("/Users/Shared/yolo/proj", jsonx.NewOrderedMap(),
		[]string{"claude"}, []string{"/bin/zsh", "-l"}, "/usr/local/bin/yolo", "",
		macosuser.HomeOverlay{}, ctx, jsonx.NewOrderedMap(), nil, nil)
	if probs := macosuser.PlanInvariants(plan); len(probs) != 0 {
		t.Fatalf("the plan fails its invariants: %v", probs)
	}
	wire := ""
	for _, a := range plan.BootstrapArgv {
		if v, ok := strings.CutPrefix(a, "YOLO_HOST_FILES="); ok {
			wire = v
		}
	}
	if !strings.Contains(wire, ".ssh") {
		t.Fatalf("YOLO_HOST_FILES does not name the directory entry: %q", wire)
	}
	t.Setenv("YOLO_CTX_ROOT", stageCtxTreeAsRoot(t, ctx.Tree))

	base := filepath.Join(home, "sandbox")
	jailHome, ws := filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{jailHome, ws} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := entrypoint.DarwinEnvFrom(map[string]string{
		"HOME":                          jailHome,
		"JAIL_HOME":                     jailHome,
		"YOLO_HOST_DIR":                 ws,
		"YOLO_BLOCK_CONFIG":             `[]`,
		"YOLO_MISE_TOOLS":               `{}`,
		"YOLO_DARWIN_WORKSPACE":         ws,
		entrypoint.DarwinHomeSidecarEnv: filepath.Join(ws, ".yolo", "home"),
		"MISE_DATA_DIR":                 filepath.Join(jailHome, ".yolo", "mise"),
		"YOLO_HOST_FILES":               wire,
	}, jailHome)
	var said bytes.Buffer
	e.Stderr = &said
	// The bootstrap reads the pack tree through LoadJailPacks, which switches the process to the
	// tolerant manifest decoder; strictPackloadReads restores strict host reads at test cleanup.
	strictPackloadReads(t)
	if err := entrypoint.RunDarwinBootstrap(e, entrypoint.DarwinBootstrapOptions{MacosLog: "off"}); err != nil {
		for _, step := range []string{"darwin_home_layout", "configure_host_files"} {
			if strings.Contains(err.Error(), step) {
				t.Fatalf("the bootstrap's %s step failed: %v\n%s", step, err, said.String())
			}
		}
	}

	for rel, mode := range files {
		laid := filepath.Join(jailHome, ".ssh", filepath.FromSlash(rel))
		if got := readOrAbsentAt(t, laid); got != rel+"\n" {
			t.Errorf("~/.ssh/%s in the sandbox home = %q, want the host file's bytes", rel, got)
			continue
		}
		fi, err := os.Stat(laid)
		if err != nil {
			t.Fatal(err)
		}
		if want := umaskedPerm(t, mode); fi.Mode().Perm() != want {
			t.Errorf("~/.ssh/%s arrived %#o, want the host file's %#o (as umask leaves it, %#o)",
				rel, fi.Mode().Perm(), mode, want)
		}
	}
}

// A GLOBAL GITIGNORE THAT CANNOT BE COPIED IS WARNED ABOUT AND THE LAUNCH GOES ON: git identity is
// never fatal (configureGit's rule), so the sandbox's git loses the host's excludes and nothing
// else, and the warning says what to do. Make the copy failure end the launch, or drop the
// warning, and this fails.
func TestMacosUserLaunchGoesOnWithoutAGlobalGitignoreItCannotCopy(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file, so this cannot make one it cannot copy")
	}
	home := ctxLaunchHome(t, "")
	ignore := filepath.Join(home, ".config", "git", "ignore")
	writeHostFileAt(t, ignore, "*.yolo-probe\n", 0o644)
	if err := os.Chmod(ignore, 0o000); err != nil {
		t.Fatal(err)
	}

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	for _, want := range []string{"Warning: your global gitignore " + ignore + " was not copied for the sandbox",
		"core.excludesFile"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch did not say %q:\n%s", want, out)
		}
	}
	if ctx.GlobalGitignore != "" {
		t.Errorf("GlobalGitignore = %q for a gitignore that was not copied: the bootstrap would point "+
			"git at a file that is not there", ctx.GlobalGitignore)
	}
}

// A DIRECTORY ENTRY WHOSE SOURCE IS THERE AND DOES NOT COPY WHOLE ENDS THE LAUNCH, naming the entry:
// a partial copy would hand the agent a directory that looks like the user's and is not (the file
// branch's fail-closed rule). An unreadable file and an unreadable folder inside the source, each
// apart from the cap, which has its own test. Treat any non-cap failure as success and this fails.
func TestMacosUserRefusesADirectoryHostFileItCannotCopyWhole(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file and lists a 0000 folder, so this cannot make one it cannot copy")
	}
	for _, tc := range []struct {
		name, locked string
		dir          bool
	}{
		{"an unreadable file", "locked.txt", false},
		{"an unreadable folder", "locked", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := ctxLaunchHome(t, `, "host_files": [{"path": ".config/themes/", "source": "~/themes/"}]`)
			src := filepath.Join(home, "themes")
			writeHostFileAt(t, filepath.Join(src, "inner.txt"), "INNER\n", 0o644)
			locked := filepath.Join(src, tc.locked)
			if tc.dir {
				writeHostFileAt(t, filepath.Join(locked, "deep.txt"), "DEEP\n", 0o644)
			} else {
				writeHostFileAt(t, locked, "L\n", 0o644)
			}
			if err := os.Chmod(locked, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

			out := runMacosUserExpectingRefusal(t, t.TempDir(), nil)

			for _, want := range []string{
				"host_files ~/.config/themes (source " + src,
				"could not be copied for the macos-user sandbox",
				"Make every file under it readable to you, or remove the entry",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, out)
				}
			}
		})
	}
}

// A FILE ENTRY'S HOST-SIDE COPY IS NEVER WIDER THAN ITS SOURCE either: copyCtxFile used to write
// every copy 0644 (0755 with an exec bit), so a 0600 ~/.aws/credentials sat readable by any account
// that can reach the user's yolo state dir, from the launch until the next one. The exec bit still
// crosses (TestMacosUserCtxTreeCarriesTheExecBit).
func TestMacosUserCtxTreeNeverWidensAHostFile(t *testing.T) {
	home := ctxLaunchHome(t, `, "host_files": [{"path": ".aws/credentials", "source": "~/.aws/credentials"}, `+
		`{"path": ".local/bin/mine", "source": "~/mine.sh"}]`)
	writeHostFileAt(t, filepath.Join(home, ".aws", "credentials"), "[default]\n", 0o600)
	writeHostFileAt(t, filepath.Join(home, "mine.sh"), "#!/bin/sh\n", 0o700)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if len(ctx.HostFiles) != 2 {
		t.Fatalf("HostFiles = %+v, want the two entries", ctx.HostFiles)
	}
	for _, e := range ctx.HostFiles {
		staged := filepath.Join(ctx.Tree, "host-user", e.Slug())
		fi, err := os.Stat(staged)
		if err != nil {
			t.Fatalf("stat %s: %v", staged, err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("the host-side copy of the owner-only ~/%s is %v", e.Path, fi.Mode().Perm())
		}
	}
}

// The modes record's directory can never be a host_files entry's: it is inside the reserved
// /ctx/host-user, and no destination's slug spells it, so a record and a staged copy never
// collide.
func TestTheHostFileModesDirIsNoHostFilesSlug(t *testing.T) {
	if path.Dir(paths.ContextHostFileModesDir) != paths.ContextHostUserDir {
		t.Fatalf("%s is not directly under %s, the reserved root", paths.ContextHostFileModesDir, paths.ContextHostUserDir)
	}
	if p, ok := config.HostFilePathFromSlug(path.Base(paths.ContextHostFileModesDir)); ok {
		t.Errorf("%q is the slug of the host_files destination ~/%s, so the two would collide",
			path.Base(paths.ContextHostFileModesDir), p)
	}
	if got := entrypoint.HostFileDirModesPath(".ssh"); path.Dir(got) != paths.ContextHostFileModesDir {
		t.Errorf("the record for .ssh is at %s, outside %s", got, paths.ContextHostFileModesDir)
	}
}
