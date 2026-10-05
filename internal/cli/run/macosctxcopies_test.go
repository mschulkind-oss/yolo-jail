package run

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosctxcopies_test.go pins the two host-to-sandbox COPIES the macos-user context tree gained
// beside DP-L1's file copies: a selected pack's single-FILE `mount` (context-mounts.md CX-D23),
// and the host's global gitignore. Both through a real Run() on the macos-user arm, for
// macosctxtree_test.go's reason: the arm returns above runContainer, so a copy the composer can
// make and the arm never asks for is the B-0 shape this backend has shipped three times.

// fileMountPack writes a configured pack `name` declaring the given contributions and returns
// its directory, for a `file://` entry in the user's `packs`.
func fileMountPack(t *testing.T, name, contributes string) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), name)
	writeHostFileAt(t, filepath.Join(src, "pack.json"),
		`{"name":"`+name+`","contributes":[`+contributes+`]}`, 0o644)
	return src
}

// homeSiting is a default macOS install's siting with no writable places and the users root at
// home's parent, so home is a REAL HOME to the siting rules: a link into it refuses (OQ-CX7),
// which is what makes a file grant's copy, and only its copy, deliverable from there.
func homeSiting(t *testing.T, home string) func(*Options) {
	return func(o *Options) {
		s := sitingWritable(t, "")
		s.UsersRoot, s.UsersRootAliases = filepath.Dir(home), nil
		o.macosCtxSiting = s
	}
}

// A PACK'S SINGLE-FILE `mount` IS COPIED, from a path in the user's home a link could not serve
// here, to the grant's own /ctx path in the composed tree: the bytes and the exec bit, recorded
// in Copied and never as a link. The banner still discloses the host read, which really happens.
// Delete the copy loop from buildMacosCtxTree, or stop the arm threading the decider's copies to
// it, and the tree holds nothing at /ctx/acme/notes.txt.
func TestMacosUserCopiesAPackFileMountIntoTheContextTree(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes/acme.sh","into":"acme/notes.sh"},`+
		`{"kind":"env","vars":{"ACME_MARKER":"1"}}`)
	writeUserPacks(t, home, `["file://`+pack+`"]`)
	writeHostFileAt(t, filepath.Join(home, "notes", "acme.sh"), "#!/bin/sh\necho NOTES\n", 0o755)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), homeSiting(t, home))

	if want := "/ctx/acme/notes.sh"; len(ctx.Copied) != 1 || ctx.Copied[0].Dest != want ||
		ctx.Copied[0].Source != filepath.Join(home, "notes", "acme.sh") || ctx.Copied[0].Pack != "acme" {
		t.Fatalf("Copied = %+v, want pack acme's ~/notes/acme.sh at %s — the pack's file grant did "+
			"not cross\n%s", ctx.Copied, want, out)
	}
	staged := filepath.Join(ctx.Tree, "acme", "notes.sh")
	if got := readOrAbsentAt(t, staged); got != "#!/bin/sh\necho NOTES\n" {
		t.Errorf("the copied file holds %q", got)
	}
	if fi, err := os.Stat(staged); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the copied file lost its exec bit (%v, %v)", fi, err)
	}
	if len(ctx.Links) != 0 {
		t.Errorf("a file grant crossed as a link too: %+v", ctx.Links)
	}
	if containsString(ctx.Delivered, "/ctx/acme/notes.sh") {
		t.Errorf("the copy was recorded as a host-layer delivery (%v); it is no pack's host layer", ctx.Delivered)
	}
	disclosed := false
	for _, line := range strings.Split(out, "\n") {
		disclosed = disclosed || (strings.Contains(line, "acme:") && strings.Contains(line, "[mount]"))
	}
	if !disclosed {
		t.Errorf("the banner no longer discloses the pack's host read:\n%s", out)
	}
	if strings.Contains(out, "Refusing the macos-user launch") {
		t.Errorf("a copied file grant refused the launch:\n%s", out)
	}
}

// THE DIRECTORY GRANT KEEPS THE LINK, and with it the refusal for a source inside a home: only a
// FILE is copied.
func TestMacosUserStillRefusesAPackDirectoryMountInAHome(t *testing.T) {
	home := acmeMountPack(t, true)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), homeSiting(t, home))

	if want := "is inside the home folder " + home; !strings.Contains(out, want) {
		t.Errorf("the refusal did not say %q:\n%s", want, out)
	}
}

// A LINK AT, INSIDE OR AROUND A COPY REFUSES: a config `mounts` element at /ctx/acme would be
// staged where the copy's directory is.
func TestMacosUserRefusesAConfigMountAroundACopiedPackFile(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"acme/notes.txt"}`)
	lib := filepath.Join(floortest.ResolvedTemp(t), "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	writeUserConfig(t, home, `{"packs": ["file://`+pack+`"], "mounts": ["`+lib+`:/ctx/acme"]}`)
	writeHostFileAt(t, filepath.Join(home, "notes.txt"), "N\n", 0o644)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), deliveringSiting(t, ""))

	for _, want := range []string{
		"contains /ctx/acme/notes.txt, which yolo's own staging uses", // the link's own side
		"where a link is staged", // the copy's side
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal did not say %q:\n%s", want, out)
		}
	}
}

// A COPY AT A SELECTED PACK'S `reads-host` DESTINATION REFUSES, against what the pack declares
// whether or not this home holds the file.
func TestMacosUserRefusesACopiedPackFileAtAReadsHostDestination(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"host-claude/settings.json"}`)
	writeUserPacks(t, home, `["claude", "file://`+pack+`"]`)
	writeHostFileAt(t, filepath.Join(home, "notes.txt"), "N\n", 0o644)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), deliveringSiting(t, ""))

	if want := "is /ctx/host-claude/settings.json, which yolo's own staging uses"; !strings.Contains(out, want) {
		t.Errorf("the refusal did not say %q:\n%s", want, out)
	}
}

// TWO PACKS COPYING TO ONE PATH REFUSE, on the later one, naming the earlier.
func TestMacosUserRefusesTwoPackFilesCopiedToOnePath(t *testing.T) {
	home := packHome(t)
	a := fileMountPack(t, "alpha", `{"kind":"mount","host":"a.txt","into":"shared/notes.txt"}`)
	b := fileMountPack(t, "beta", `{"kind":"mount","host":"b.txt","into":"shared/notes.txt"}`)
	writeUserPacks(t, home, `["file://`+a+`", "file://`+b+`"]`)
	writeHostFileAt(t, filepath.Join(home, "a.txt"), "A\n", 0o644)
	writeHostFileAt(t, filepath.Join(home, "b.txt"), "B\n", 0o644)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), deliveringSiting(t, ""))

	if want := "two copied files cannot share or nest a path"; !strings.Contains(out, want) {
		t.Errorf("the refusal did not say %q:\n%s", want, out)
	}
}

// AN ABSENT FILE IS SKIPPED with the container backends' line, and the launch goes on (CX-D9).
func TestMacosUserSkipsAPackFileMountWhoseSourceIsAbsent(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"acme/notes.txt"}`)
	writeUserPacks(t, home, `["file://`+pack+`"]`)

	ctx, out := runMacosUserCapturingCtx(t, t.TempDir(), homeSiting(t, home))

	if !strings.Contains(out, "mount source does not exist, skipping: ~/notes.txt") {
		t.Errorf("the absent source was not named as skipped:\n%s", out)
	}
	if len(ctx.Copied) != 0 || ctx.Tree != "" {
		t.Errorf("an absent file crossed: Copied=%v Tree=%q", ctx.Copied, ctx.Tree)
	}
}

// A FILE THAT IS THERE AND CANNOT BE READ ENDS THE LAUNCH, with the next steps: the copy runs as
// the user, so on a Mac the usual cause is a folder macOS's privacy controls guard.
func TestMacosUserRefusesAPackFileMountItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a 0000 file, so this cannot make one it cannot copy")
	}
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"acme/notes.txt"}`)
	writeUserPacks(t, home, `["file://`+pack+`"]`)
	writeHostFileAt(t, filepath.Join(home, "notes.txt"), "N\n", 0o000)

	out := runMacosUserExpectingRefusal(t, t.TempDir(), homeSiting(t, home))

	for _, want := range []string{"pack acme's `mount` ~/notes.txt → /ctx/acme/notes.txt could not be copied",
		"Privacy & Security", "remove the pack for this workspace", "container runtime"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal did not say %q:\n%s", want, out)
		}
	}
}

// THE BRIEFING LISTS THE COPY where the agent opens it, under the context dir, MARKED copied, so
// the agent is told a host edit arrives at the next launch rather than at once. Delete the copy
// arm from briefedCtxMounts and the entry is gone.
func TestMacosUserBriefsACopiedPackFileMount(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"acme/notes.txt"}`)
	writeUserPacks(t, home, `["file://`+pack+`"]`)
	writeHostFileAt(t, filepath.Join(home, "notes.txt"), "N\n", 0o644)
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	_, loaded, _, err := o.stagePacks("yolo-test-ctxcopy-brief")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	homeSiting(t, home)(o)
	const ctxDir = "/var/yolo-jail/ctx/yolo-ws-abcd1234"

	got := o.briefedCtxMounts("macos-user", ctxDir, jsonx.NewOrderedMap(), loaded)

	if len(got) != 1 || !got[0].Copied || got[0].Path != ctxDir+"/acme/notes.txt" ||
		got[0].Pack != "acme" || got[0].Host != filepath.Join(home, "notes.txt") {
		t.Errorf("briefed %+v, want the one copied entry at %s/acme/notes.txt from pack acme", got, ctxDir)
	}
}

// THE HOST'S GLOBAL GITIGNORE CROSSES TO THE SANDBOX, copied into the tree at its reserved path
// and recorded for the plan, which names it to the bootstrap. Git's own default location here,
// because the stub host git answers nothing. Never a host-layer delivery: it is identity.
func TestMacosUserCopiesTheGlobalGitignoreIntoTheContextTree(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, ".config", "git", "ignore"), "*.yolo-probe\n", 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if ctx.GlobalGitignore != paths.ContextGlobalGitignore {
		t.Fatalf("GlobalGitignore = %q, want %q — the sandbox's git would ignore only what each "+
			"repository lists", ctx.GlobalGitignore, paths.ContextGlobalGitignore)
	}
	staged := filepath.Join(ctx.Tree, strings.TrimPrefix(paths.ContextGlobalGitignore, paths.ContainerContextDir+"/"))
	if got := readOrAbsentAt(t, staged); got != "*.yolo-probe\n" {
		t.Errorf("the staged gitignore holds %q", got)
	}
	if containsString(ctx.Delivered, paths.ContextGlobalGitignore) {
		t.Errorf("the gitignore was recorded as a host-layer delivery: %v", ctx.Delivered)
	}
}

// git's `core.excludesFile` decides, `~`-expanded, as it does for the container's bind
// (hostGlobalGitignore is the one reader of both).
func TestMacosUserCopiesTheGlobalGitignoreGitNames(t *testing.T) {
	home := ctxLaunchHome(t, "")
	writeHostFileAt(t, filepath.Join(home, "my-ignore"), "MINE\n", 0o644)
	writeHostFileAt(t, filepath.Join(home, ".config", "git", "ignore"), "DEFAULT\n", 0o644)

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), func(o *Options) {
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if strings.Join(argv, " ") == "git config --global --get core.excludesFile" {
				return ExecResult{Ran: true, Stdout: "~/my-ignore\n"}
			}
			return ExecResult{Ran: false}
		}
	})

	staged := filepath.Join(ctx.Tree, strings.TrimPrefix(paths.ContextGlobalGitignore, paths.ContainerContextDir+"/"))
	if got := readOrAbsentAt(t, staged); got != "MINE\n" {
		t.Errorf("the staged gitignore holds %q, want the file core.excludesFile names", got)
	}
}

// No gitignore, nothing staged for it — and, with nothing else to carry, no tree at all.
func TestMacosUserStagesNoGlobalGitignoreWhenThereIsNone(t *testing.T) {
	ctxLaunchHome(t, "")

	ctx, _ := runMacosUserCapturingCtx(t, t.TempDir(), nil)

	if ctx.GlobalGitignore != "" || ctx.Tree != "" {
		t.Errorf("GlobalGitignore = %q, Tree = %q with no gitignore on this host", ctx.GlobalGitignore, ctx.Tree)
	}
}

// UNDER THE SEAL (FP-D11) no gitignore is read, as the container launch reads none.
func TestASealedLaunchCopiesNoGlobalGitignore(t *testing.T) {
	home := packHome(t)
	writeHostFileAt(t, filepath.Join(home, ".config", "git", "ignore"), "*.x\n", 0o644)
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }, Sealed: true}

	delivery, err := o.buildMacosCtxTree(t.TempDir(), nil, jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	if delivery.ctx.GlobalGitignore != "" || delivery.ctx.Tree != "" {
		t.Errorf("a sealed launch staged the host's gitignore: %+v", delivery.ctx)
	}
}

// The gitignore's name can never be a host_files entry's: it is inside the reserved
// /ctx/host-user, and no destination's slug spells it.
func TestTheGlobalGitignoreNameIsNoHostFilesSlug(t *testing.T) {
	if path.Dir(paths.ContextGlobalGitignore) != paths.ContextHostUserDir {
		t.Fatalf("%s is not directly under %s, the reserved root", paths.ContextGlobalGitignore, paths.ContextHostUserDir)
	}
	if p, ok := config.HostFilePathFromSlug(path.Base(paths.ContextGlobalGitignore)); ok {
		t.Errorf("%q is the slug of the host_files destination ~/%s, so the two would collide",
			path.Base(paths.ContextGlobalGitignore), p)
	}
}

// The decider's two outputs stay apart: a file grant is a copy and never a link, a directory
// grant a link and never a copy, whatever the siting says about either.
func TestTheMacosUserDeciderSplitsFileGrantsFromDirectoryGrants(t *testing.T) {
	home := packHome(t)
	pack := fileMountPack(t, "acme", `{"kind":"mount","host":"notes.txt","into":"acme/notes.txt"},`+
		`{"kind":"mount","host":"data","into":"acme-data"}`)
	writeUserPacks(t, home, `["file://`+pack+`"]`)
	writeHostFileAt(t, filepath.Join(home, "notes.txt"), "N\n", 0o644)
	if err := os.MkdirAll(filepath.Join(home, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	_, loaded, _, err := o.stagePacks("yolo-test-ctxcopy-split")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	deliveringSiting(t, "")(o)

	links, copies, refused := o.macosCtxLinks(jsonx.NewOrderedMap(), loaded, nil)

	if len(refused) != 0 {
		t.Fatalf("refused %+v", refused)
	}
	wantCopy := macosuser.ContextLink{Dest: "/ctx/acme/notes.txt", Source: filepath.Join(home, "notes.txt"),
		Named: "~/notes.txt", Pack: "acme"}
	if len(copies) != 1 || copies[0] != wantCopy {
		t.Errorf("copies = %+v, want %+v", copies, wantCopy)
	}
	if len(links) != 1 || links[0].Dest != "/ctx/acme-data" || !links[0].Dir {
		t.Errorf("links = %+v, want the directory grant alone", links)
	}
}
