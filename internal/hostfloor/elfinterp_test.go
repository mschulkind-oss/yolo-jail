package hostfloor

// elfinterp_test.go pins HP-D15: a program asking for a dynamic loader this machine lacks — or one
// NixOS's stub-ld stands in for — has no floor entry, decided before anything is downloaded or
// materialized where the floor can tell, and after the install where it cannot. Every test puts the
// floor on Linux under a filesystem root of its own (Floor.Root), so the answer never depends on what
// the machine running it keeps in /lib64, and the test runs on a Mac too. No synthetic ELF here is
// ever executed: the loader it names does not exist.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
)

// syntheticELF is a minimal ELF executable of the given class (1 = 32-bit, 2 = 64-bit) and byte
// order whose program headers are one PT_LOAD and, when interp is not "", a PT_INTERP naming it. No
// sections, so debug/elf reads nothing this reader does not.
func syntheticELF(class byte, order binary.ByteOrder, interp string) []byte {
	ehsize, phentsize := 64, 56
	if class == 1 {
		ehsize, phentsize = 52, 32
	}
	phnum := 1
	if interp != "" {
		phnum = 2
	}
	interpOff := ehsize + phnum*phentsize
	body := append([]byte(interp), 0)
	buf := make([]byte, interpOff+len(body))
	data := byte(1)
	if order == binary.BigEndian {
		data = 2
	}
	copy(buf, []byte{0x7f, 'E', 'L', 'F', class, data, 1 /*EV_CURRENT*/})
	order.PutUint16(buf[16:], 2) // ET_EXEC
	if class == 1 {
		order.PutUint16(buf[18:], 3) // EM_386
		order.PutUint32(buf[20:], 1)
		order.PutUint32(buf[28:], uint32(ehsize)) // e_phoff
		order.PutUint16(buf[40:], uint16(ehsize))
		order.PutUint16(buf[42:], uint16(phentsize))
		order.PutUint16(buf[44:], uint16(phnum))
	} else {
		order.PutUint16(buf[18:], 62) // EM_X86_64
		order.PutUint32(buf[20:], 1)
		order.PutUint64(buf[32:], uint64(ehsize)) // e_phoff
		order.PutUint16(buf[52:], uint16(ehsize))
		order.PutUint16(buf[54:], uint16(phentsize))
		order.PutUint16(buf[56:], uint16(phnum))
	}
	ph := func(i int, typ uint32, off, size uint64) {
		p := buf[ehsize+i*phentsize:]
		order.PutUint32(p[0:], typ)
		if class == 1 {
			order.PutUint32(p[4:], uint32(off))
			order.PutUint32(p[16:], uint32(size))
			order.PutUint32(p[20:], uint32(size))
			order.PutUint32(p[24:], 4) // PF_R
			order.PutUint32(p[28:], 1)
		} else {
			order.PutUint32(p[4:], 4) // PF_R
			order.PutUint64(p[8:], off)
			order.PutUint64(p[32:], size)
			order.PutUint64(p[40:], size)
			order.PutUint64(p[48:], 1)
		}
	}
	ph(0, 1 /*PT_LOAD*/, 0, uint64(len(buf)))
	if interp != "" {
		ph(1, 3 /*PT_INTERP*/, uint64(interpOff), uint64(len(body)))
	}
	copy(buf[interpOff:], body)
	return buf
}

// elfAsking is a 64-bit little-endian program asking for loader ("" for a static one).
func elfAsking(loader string) []byte { return syntheticELF(2, binary.LittleEndian, loader) }

// linuxLoader is the loader Node's official build for this floor's platform asks for.
func (w *world) linuxLoader() string { return officialNodeLoader[w.plat] }

// rootWithout points the floor at a filesystem root that holds no loader at all.
func (w *world) rootWithout() string {
	w.t.Helper()
	w.floor.Root = filepath.Join(resolvedTemp(w.t), "bare-root")
	must(w.t, os.MkdirAll(w.floor.Root, 0o755))
	return w.floor.Root
}

// rootLinking points the floor at a root whose loader is an ABSOLUTE link to target — read from that
// root too, as the kernel reads one in a chroot — with a file at target, and returns the root.
func (w *world) rootLinking(target string) string {
	w.t.Helper()
	root := w.rootWithout()
	loader := filepath.Join(root, filepath.FromSlash(w.linuxLoader()))
	must(w.t, os.MkdirAll(filepath.Dir(loader), 0o755))
	must(w.t, os.Symlink(target, loader))
	file := filepath.Join(root, filepath.FromSlash(target))
	must(w.t, os.MkdirAll(filepath.Dir(file), 0o755))
	must(w.t, os.WriteFile(file, []byte("a loader\n"), 0o755))
	return root
}

// addELFCapture admits a capture of bin at version shaped as add's, its program a synthetic ELF
// asking for loader.
func (c *captureStore) addELFCapture(bin, version, loader string) *capture.Entry {
	c.t.Helper()
	program := elfAsking(loader)
	return c.addShaped(bin, version, true, func(tree string, m *capture.Manifest) {
		rel := ".local/share/" + bin + "/versions/" + version
		must(c.t, os.WriteFile(filepath.Join(tree, filepath.FromSlash(rel)), program, 0o755))
		for i := range m.Entries {
			if m.Entries[i].Path == rel {
				m.Entries[i].Size = int64(len(program))
			}
		}
	})
}

// TestTheLoaderReaderAgreesWithDebugELF: the floor's own PT_INTERP reader gives the standard
// library's answer for both classes and both byte orders, with and without a loader, and for this
// machine's own programs; and it calls a script, the fake shell node among them, not ELF.
func TestTheLoaderReaderAgreesWithDebugELF(t *testing.T) {
	dir := resolvedTemp(t)
	files := map[string][]byte{
		"elf64le":        syntheticELF(2, binary.LittleEndian, "/lib64/ld-linux-x86-64.so.2"),
		"elf64be":        syntheticELF(2, binary.BigEndian, "/lib/ld64.so.1"),
		"elf32le":        syntheticELF(1, binary.LittleEndian, "/lib/ld-linux.so.2"),
		"elf64le-static": syntheticELF(2, binary.LittleEndian, ""),
	}
	var paths []string
	for name, b := range files {
		p := filepath.Join(dir, name)
		must(t, os.WriteFile(p, b, 0o755))
		paths = append(paths, p)
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, exe)
	}
	if sh, err := filepath.EvalSymlinks("/bin/sh"); err == nil {
		paths = append(paths, sh)
	}
	for _, p := range paths {
		want, wantErr := interpViaDebugELF(p)
		got, err := elfInterp(p)
		if wantErr != nil {
			// Not ELF to the standard library (a Mach-O on a Mac): not ELF here either.
			if !errors.Is(err, errNotELF) {
				t.Errorf("%s: debug/elf says %v, elfInterp = %q, %v", p, wantErr, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%s: elfInterp = %q, %v; debug/elf = %q", p, got, err, want)
		}
	}
	script := filepath.Join(dir, "node")
	must(t, os.WriteFile(script, []byte(floortest.NodeScript), 0o755))
	if got, err := elfInterp(script); !errors.Is(err, errNotELF) {
		t.Errorf("the fake shell node: elfInterp = %q, %v, want not ELF", got, err)
	}
	// The fake npm registry's "native" package: an ELF magic and no more.
	short := filepath.Join(dir, "short")
	must(t, os.WriteFile(short, []byte("\x7fELF\x02\x01\x01\x00native-1.2.3\n"), 0o755))
	if got, err := elfInterp(short); err == nil || errors.Is(err, errNotELF) {
		t.Errorf("a truncated ELF: elfInterp = %q, %v, want a read error", got, err)
	}
}

// interpViaDebugELF is the standard library's answer: the PT_INTERP string, "" for none.
func interpViaDebugELF(p string) (string, error) {
	f, err := elf.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	for _, prog := range f.Progs {
		if prog.Type != elf.PT_INTERP {
			continue
		}
		b := make([]byte, prog.Filesz)
		if _, err := prog.ReadAt(b, 0); err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\x00"), nil
	}
	return "", nil
}

// TestALoaderIsResolvedUnderTheRootAsTheKernelResolvesIt: an absolute link is read from the root, a
// relative one from its own directory, a linked directory on the way is followed (Arch's /lib64 ->
// usr/lib), `..` stops at the root, and a loop is an error, not a hang.
func TestALoaderIsResolvedUnderTheRootAsTheKernelResolvesIt(t *testing.T) {
	root := resolvedTemp(t)
	mk := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte("x"), 0o755))
	}
	ln := func(rel, target string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.Symlink(target, p))
	}
	mk("usr/lib/ld-linux-x86-64.so.2")
	ln("lib64", "usr/lib")
	mk("nix/store/abc-nix-ld/libexec/nix-ld")
	ln("lib/ld-linux-aarch64.so.1", "/nix/store/abc-nix-ld/libexec/nix-ld")
	ln("loop/a", "b")
	ln("loop/b", "a")
	for _, c := range []struct{ p, want string }{
		{"/lib64/ld-linux-x86-64.so.2", "usr/lib/ld-linux-x86-64.so.2"},
		{"/lib/ld-linux-aarch64.so.1", "nix/store/abc-nix-ld/libexec/nix-ld"},
		{"/../../usr/lib/ld-linux-x86-64.so.2", "usr/lib/ld-linux-x86-64.so.2"},
	} {
		got, err := resolveUnder(root, c.p)
		if err != nil || got != filepath.Join(root, filepath.FromSlash(c.want)) {
			t.Errorf("resolveUnder(%s) = %q, %v, want %s under the root", c.p, got, err, c.want)
		}
	}
	if _, err := resolveUnder(root, "/loop/a"); !errors.Is(err, errLinkLoop) {
		t.Errorf("a loop: %v, want errLinkLoop", err)
	}
	if _, err := resolveUnder(root, "/lib64/missing.so"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing loader: %v, want not-exist", err)
	}
}

// (a) A MACHINE WITHOUT NODE'S LOADER: an npm program has no floor entry, and the reason names the
// loader, the two kinds of host that lack it and the nix-ld step — decided before any download. The
// distribution is an address nothing listens on, so a fetch would fail the launch rather than give
// this answer; npm never runs and the prefix is never created.
func TestAnNpmProgramOnAMachineWithoutNodesLoaderHasNoFloorEntryBeforeAnyDownload(t *testing.T) {
	w := newLinuxWorld(t)
	w.rootWithout()
	w.floor.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
	w.publish("opencode-ai", "1.2.3", "bin=opencode")
	p := npmProgram("opencode", "opencode", "opencode-ai")
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry {
		t.Fatalf("Ensure = %s (%s) %v, want no floor entry\n%s", st.Disposition, st.Reason, err, w.out.String())
	}
	for _, want := range []string{"Node's official " + w.plat + " build", "needs the dynamic loader " + w.linuxLoader(),
		"a NixOS host without nix-ld, or a musl system", "programs.nix-ld.enable = true;"} {
		if !strings.Contains(st.Reason, want) {
			t.Errorf("the reason lacks %q:\n  %s", want, st.Reason)
		}
	}
	if n := len(w.npmCalls("install")) + len(w.npmCalls("view")); n != 0 {
		t.Errorf("npm ran %d times", n)
	}
	if _, err := os.Stat(w.floor.Dir); err == nil {
		t.Errorf("the floor created %s for a program it cannot hold", w.floor.Dir)
	}
}

// (b) NIXOS'S STUB LOADER, which its stub-ld module puts at the loader's path when nix-ld is off:
// the file exists, and still no program starts, so it is no floor entry, named as the stub.
func TestNixOSsStubLoaderIsNoFloorEntry(t *testing.T) {
	w := newLinuxWorld(t)
	w.rootLinking("/nix/store/0123abcd-stub-ld")
	w.floor.Node.BaseURL = "http://127.0.0.1:1/test-guard-no-node-download"
	st := w.floor.Status(npmProgram("opencode", "opencode", "opencode-ai"))
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "NixOS's stub loader") ||
		!strings.Contains(st.Reason, "0123abcd-stub-ld") || !strings.Contains(st.Reason, "programs.nix-ld.enable = true;") {
		t.Fatalf("Status = %s (%s), want no floor entry naming the stub and the nix-ld step", st.Disposition, st.Reason)
	}
}

// (c) NIX-LD at the loader's path, a link into the store: the program installs and runs.
func TestANixLDLoaderInstalls(t *testing.T) {
	w := newLinuxWorld(t)
	w.rootLinking("/nix/store/0123abcd-nix-ld-2.0.6/libexec/nix-ld")
	w.publish("opencode-ai", "1.2.3", "bin=opencode")
	st, outcome, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
	if err != nil || outcome != Installed {
		t.Fatalf("Ensure = %s %v\n%s", outcome, err, w.out.String())
	}
	if out, err := exec.Command(st.Launcher).CombinedOutput(); err != nil {
		t.Errorf("running it: %q %v", out, err)
	}
}

// (d) A CAPTURE WHOSE PROGRAM ASKS FOR A LOADER THIS MACHINE LACKS: no floor entry, and nothing is
// materialized — judged from the store's entry before an install, and from a capture the install
// itself made before it copies anything.
func TestACaptureWhoseProgramNeedsAMissingLoaderIsNoFloorEntryWithoutAMaterialize(t *testing.T) {
	w := newLinuxWorld(t)
	w.rootWithout()
	cs := newCaptureStore(t)
	w.floor.ResolveCapture = cs.resolve
	cs.addELFCapture("claude", "2.1.267", w.linuxLoader())
	claude := installerProgram("claude", "claude")
	st := w.floor.Status(claude)
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "the capture of claude on this machine needs the dynamic loader "+
		w.linuxLoader()) {
		t.Fatalf("with the store's capture: %s (%s)", st.Disposition, st.Reason)
	}

	fresh := newCaptureStore(t)
	w.floor.ResolveCapture = fresh.resolve
	captures := 0
	w.floor.Capture = func(bin string) error {
		captures++
		fresh.addELFCapture(bin, "2.1.267", w.linuxLoader())
		return nil
	}
	st, _, err := w.floor.Ensure(context.Background(), claude)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry || captures != 1 ||
		!strings.Contains(st.Reason, "needs the dynamic loader") {
		t.Fatalf("when the install makes the capture: %s (%s) %v, %d captures\n%s", st.Disposition, st.Reason, err,
			captures, w.out.String())
	}
	if strings.Contains(w.out.String(), "materialized claude") {
		t.Errorf("a program this machine cannot start was materialized:\n%s", w.out.String())
	}
	if dirs, _ := os.ReadDir(w.floor.programsDir("claude")); len(dirs) != 0 {
		t.Errorf("the refused install left %d install directories", len(dirs))
	}
}

// (e) A PROVISIONED COPY WHOSE LOADER WENT AWAY (nix-ld turned off, say) is no floor entry from then
// on, so a launch runs the PATH copy, and `yolo host apply --assert` removes it as it does any entry
// the floor can no longer hold: an npm program (its Node's loader) and an installer's capture (its
// own program's).
func TestAProvisionedCopyWhoseLoaderWentAwayIsNoFloorEntryAndReconcileRemovesIt(t *testing.T) {
	t.Run("npm", func(t *testing.T) {
		w := newLinuxWorld(t)
		w.publish("opencode-ai", "1.2.3", "bin=opencode")
		p := npmProgram("opencode", "opencode", "opencode-ai")
		if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		must(t, os.Remove(filepath.Join(w.floor.Root, filepath.FromSlash(w.linuxLoader()))))
		assertGoneLoaderRemoved(t, w, p)
	})
	t.Run("installer", func(t *testing.T) {
		w := newLinuxWorld(t)
		cs := newCaptureStore(t)
		w.floor.ResolveCapture = cs.resolve
		cs.addELFCapture("claude", "2.1.267", w.linuxLoader())
		p := installerProgram("claude", "claude")
		if _, _, err := w.floor.Ensure(context.Background(), p); err != nil {
			t.Fatalf("Ensure: %v\n%s", err, w.out.String())
		}
		must(t, os.Remove(filepath.Join(w.floor.Root, filepath.FromSlash(w.linuxLoader()))))
		if st := assertGoneLoaderRemoved(t, w, p); !strings.Contains(st.Reason, "its copy in yolo's floor (2.1.267)") {
			t.Errorf("the reason does not name the installed copy: %s", st.Reason)
		}
	})
}

func assertGoneLoaderRemoved(t *testing.T, w *world, p Program) Status {
	t.Helper()
	st := w.floor.Status(p)
	if st.Disposition != NoEntry || !strings.Contains(st.Reason, "needs the dynamic loader "+w.linuxLoader()) {
		t.Fatalf("after the loader went: %s (%s), want no floor entry naming it", st.Disposition, st.Reason)
	}
	if _, _, err := w.floor.Ensure(context.Background(), p); !errors.Is(err, ErrNoEntry) {
		t.Errorf("Ensure = %v, want ErrNoEntry", err)
	}
	removed := w.floor.Reconcile([]Program{p}, true)
	if len(removed) != 1 || removed[0].Bin != p.Bin() || !strings.Contains(removed[0].Why, "dynamic loader") {
		t.Fatalf("Reconcile = %+v, want %s removed for its loader", removed, p.Bin())
	}
	if _, err := os.Stat(w.floor.Launcher(p.Bin())); err == nil {
		t.Errorf("%s's launcher survived the removal", p.Bin())
	}
	return st
}

// (f) A PROGRAM THAT ASKS FOR NO LOADER is untouched on a machine with none at all: a static ELF, a
// script. (The fake shell node is the same case, which every npm test here installs.)
func TestProgramsThatNeedNoLoaderAreUnaffected(t *testing.T) {
	for _, c := range []struct {
		name string
		body []byte
	}{
		{"a static ELF", elfAsking("")},
		{"a script", []byte("#!/bin/sh\necho hi\n")},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newLinuxWorld(t)
			w.rootWithout()
			cs := newCaptureStore(t)
			w.floor.ResolveCapture = cs.resolve
			cs.addShaped("claude", "2.1.267", true, func(tree string, m *capture.Manifest) {
				rel := ".local/share/claude/versions/2.1.267"
				must(t, os.WriteFile(filepath.Join(tree, filepath.FromSlash(rel)), c.body, 0o755))
				for i := range m.Entries {
					if m.Entries[i].Path == rel {
						m.Entries[i].Size = int64(len(c.body))
					}
				}
			})
			st, outcome, err := w.floor.Ensure(context.Background(), installerProgram("claude", "claude"))
			if err != nil || outcome != Installed || st.Disposition != Provisioned {
				t.Fatalf("Ensure = %s %s (%s) %v\n%s", outcome, st.Disposition, st.Reason, err, w.out.String())
			}
		})
	}
}

// A FORK'S BUILD whose program asks for a loader this machine lacks: the store's build is no floor
// entry before an install, and a build the install itself made is refused once it is in place —
// the install's own check, the one that covers every recipe — leaving no install directory.
func TestAForkBuildWhoseProgramNeedsAMissingLoaderIsNoFloorEntry(t *testing.T) {
	pin := forkCommitOne
	w, bs := forkWorld(t, &pin)
	w.rootWithout()
	p := forkProgram()
	p.Install.Produces = []string{".local/bin/forkcli"}
	program := elfAsking(w.linuxLoader())
	w.floor.Build = func(p Program, commit string) (*capture.Entry, error) {
		bs.builds = append(bs.builds, commit)
		staged, err := bs.store.Stage("elf-" + commit[:8])
		must(t, err)
		bin := filepath.Join(capture.TreeDir(staged), ".local", "bin")
		must(t, os.MkdirAll(bin, 0o755))
		must(t, os.WriteFile(filepath.Join(bin, "forkcli"), program, 0o755))
		must(t, capture.WriteManifest(staged, &capture.Manifest{
			Schema: capture.ManifestSchema, Home: "/home/agent", Platform: capture.Platform(),
			Surfaces: allSurfaces(), Excluded: []string{},
			Entries: []capture.ManifestEntry{
				{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
				{Path: ".local/bin/forkcli", Kind: capture.KindFile, Mode: "0755", Size: int64(len(program))},
			},
			AbsoluteRefs: []capture.AbsoluteRef{}, RefScan: capture.RefScanFull, Relocatable: true,
		}))
		entry, err := bs.store.AdmitEntry(staged)
		must(t, err)
		bs.byPin[bs.keyOf(p, commit)] = entry
		return entry, nil
	}
	st, _, err := w.floor.Ensure(context.Background(), p)
	if !errors.Is(err, ErrNoEntry) || st.Disposition != NoEntry || !strings.Contains(st.Reason, "needs the dynamic loader") {
		t.Fatalf("the install's build: %s (%s) %v\n%s", st.Disposition, st.Reason, err, w.out.String())
	}
	if dirs, _ := os.ReadDir(w.floor.programsDir("forkcli")); len(dirs) != 0 {
		t.Errorf("the refused install left %d install directories", len(dirs))
	}
	if _, err := os.Stat(w.floor.Launcher("forkcli")); err == nil {
		t.Error("a launcher was written for a program this machine cannot start")
	}
	// The build is in the store now: the next status says so before any install.
	if st := w.floor.Status(p); st.Disposition != NoEntry || !strings.Contains(st.Reason, "needs the dynamic loader") {
		t.Errorf("with the build in the store: %s (%s)", st.Disposition, st.Reason)
	}
	if len(bs.builds) != 1 {
		t.Errorf("%d builds, want the one", len(bs.builds))
	}
}

// A NODE WHOSE LOADER IS NOT THE ONE YOLO EXPECTS is not installed: the check that let this machine
// fetch it read the loader compiled into yolo, so a release that asks for another one is a fact
// yolo cannot vouch for. One that asks for the expected loader installs.
func TestAnExtractedNodeIsCheckedAgainstTheLoaderYoloExpects(t *testing.T) {
	for _, c := range []struct {
		name, loader string
		ok           bool
	}{
		{"the expected loader", "", true},
		{"another loader", "/lib/ld-musl-x86_64.so.1", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newLinuxWorld(t)
			loader := c.loader
			if loader == "" {
				loader = w.linuxLoader()
			}
			tarball := elfNodeTarball(t, floortest.Shipped, w.plat, elfAsking(loader))
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/dist/v"+floortest.Shipped+"/node-v"+floortest.Shipped+"-"+w.plat+".tar.gz" {
					_, _ = rw.Write(tarball)
					return
				}
				http.NotFound(rw, r)
			}))
			t.Cleanup(srv.Close)
			w.floor.Node = NodeDist{BaseURL: srv.URL + "/dist", Shipped: floortest.Shipped,
				Pinned: map[string]string{w.plat: floortest.Sum(tarball)}}
			w.publish("opencode-ai", "1.2.3", "bin=opencode")
			_, outcome, err := w.floor.Ensure(context.Background(), npmProgram("opencode", "opencode", "opencode-ai"))
			if c.ok {
				if err != nil || outcome != Installed {
					t.Fatalf("Ensure = %s %v\n%s", outcome, err, w.out.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), `asks for the dynamic loader "`+loader+`"`) ||
				!strings.Contains(err.Error(), "yolo update") {
				t.Fatalf("Ensure = %v, want the refusal naming the loader and the update", err)
			}
			if w.floor.NodeReady(floortest.Shipped) {
				t.Error("a Node release yolo could not vouch for was left in the floor")
			}
		})
	}
}

// elfNodeTarball is floortest.NodeTarball with node an ELF of the caller's: a release shaped as
// Node's, whose npm is the fake registry's.
func elfNodeTarball(t *testing.T, version, plat string, node []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	top := "node-v" + version + "-" + plat + "/"
	for _, f := range []struct {
		name string
		typ  byte
		body []byte
		link string
	}{
		{name: "", typ: tar.TypeDir},
		{name: "bin/", typ: tar.TypeDir},
		{name: "bin/node", typ: tar.TypeReg, body: node},
		{name: "lib/node_modules/npm/bin/npm-cli.js", typ: tar.TypeReg, body: []byte(floortest.NpmScript)},
		{name: "bin/npm", typ: tar.TypeSymlink, link: "../lib/node_modules/npm/bin/npm-cli.js"},
	} {
		must(t, tw.WriteHeader(&tar.Header{Name: top + f.name, Typeflag: f.typ, Mode: 0o755,
			Size: int64(len(f.body)), Linkname: f.link}))
		if len(f.body) > 0 {
			if _, err := tw.Write(f.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

// NO LOADER IS CHECKED off a Linux floor, or for a Linux floor on a machine that is not Linux unless
// the caller chose the root: a Mac testing a Linux floor would otherwise read its own /lib64.
func TestTheLoaderCheckRunsOnlyForALinuxFloor(t *testing.T) {
	bare := resolvedTemp(t)
	for _, c := range []struct {
		goos, root string
		want       bool
	}{
		{"darwin", bare, false},
		{"linux", bare, true},
		{"linux", "", runtime.GOOS == "linux"},
	} {
		f := &Floor{GOOS: c.goos, Root: c.root}
		if got := f.probesLoaders(); got != c.want {
			t.Errorf("GOOS %s, Root %q: probes = %v, want %v", c.goos, c.root, got, c.want)
		}
		if got := f.loaderProblem("/lib64/ld-linux-x86-64.so.2") != ""; c.root != "" && got != c.want {
			t.Errorf("GOOS %s under an empty root: a problem = %v, want %v", c.goos, got, c.want)
		}
	}
}
