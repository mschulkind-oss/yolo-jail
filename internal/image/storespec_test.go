package image

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file covers the second half of the store-write decision (storespec.go):
// WHICH containers-storage the copier writes. Issue #47 is the reason it exists.
// On a rootless podman whose only storage.conf is the distro's root-path
// /usr/share/containers/storage.conf (stock Ubuntu 26.04), podman and the copier
// resolved DIFFERENT stores from the same files, and every launch died with
// `mkdir /run/containers: permission denied`. A rootless copy now writes the store
// podman itself reports, named explicitly on the destination; a rootful one keeps
// the bare destination. The real copier's side of that (a named store overrides
// storage.conf) is pinned in integration/storespec_copier_test.go.

// rootlessInfo is SYNTHETIC `podman info --format json` shaped like the reporter's
// host as the issue describes it (podman read the distro's /usr/share file and
// still reported ROOTLESS paths); it was not captured there. Trimmed to the keys
// the delivery reads plus a neighbor or two, so a decoder that matched the first
// "graphRoot"-shaped string anywhere would be caught.
const rootlessInfo = `{
  "host": {"arch": "amd64", "security": {"rootless": true, "seccompEnabled": true}},
  "store": {
    "configFile": "/usr/share/containers/storage.conf",
    "graphDriverName": "overlay",
    "graphOptions": {},
    "graphRoot": "/home/u/.local/share/containers/storage",
    "imageStore": {"number": 0},
    "runRoot": "/run/user/1000/containers"
  },
  "version": {"Version": "5.7.0"}
}`

// realRootfulInfo is VERBATIM `podman info --format json` (podman 5.8.6, the
// nested jail this was written in), cut to host.security and the whole store
// section: the mount_program object with its multi-line Version, the imageStore
// count object and the other keys the decoder must step over.
func realRootfulInfo(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "podman-info-rootful-nested-jail.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const realFuseOverlayfs = "/nix/store/jxi4gzz2pawg2pppdd3avm7yccpfs8jy-fuse-overlayfs-1.18/bin/fuse-overlayfs"

func factsFrom(t *testing.T, stdout string) PodmanStoreFacts {
	t.Helper()
	calls := 0
	f := ReadPodmanStoreFacts("podman", func(argv []string) (string, bool) {
		calls++
		if strings.Join(argv, " ") != "podman info --format json" {
			t.Errorf("probe argv = %q, want `podman info --format json`", argv)
		}
		return stdout, true
	})
	if calls != 1 {
		t.Errorf("podman info ran %d times, want exactly one read", calls)
	}
	return f
}

// TestOnlyARootlessDestinationNamesTheStore is the fix as a table: a ROOTLESS
// podman's store is named on the destination, so the copy does not depend on the
// copier's own config lookup agreeing with podman's; a ROOTFUL or UNKNOWN answer
// keeps the bare destination, which root resolves the same way in both and which
// names none of the config the spec would drop.
func TestOnlyARootlessDestinationNamesTheStore(t *testing.T) {
	const ref = "localhost/yolo-jail:0123456789abcdef"
	unknownWithStore := `{"host":{"security":{}},"store":{"graphDriverName":"overlay",` +
		`"graphRoot":"/g","runRoot":"/r"}}`
	for _, tc := range []struct {
		name, info, want string
		rootless         PodmanRootless
	}{
		{"rootless", rootlessInfo,
			"containers-storage:[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers]" + ref,
			RootlessYes},
		{"rootful", realRootfulInfo(t), "containers-storage:" + ref, RootlessNo},
		{"rootlessness unknown", unknownWithStore, "containers-storage:" + ref, RootlessUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := factsFrom(t, tc.info)
			if f.Rootless != tc.rootless {
				t.Errorf("rootless = %v, want %v", f.Rootless, tc.rootless)
			}
			if !f.StoreKnown {
				t.Fatalf("store not read from podman info: %s", f.Unknown)
			}
			if got := ContainersStorageDestFor(f, ref); got != tc.want {
				t.Errorf("dest =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

// TestRealPodmanInfoDecodesToItsStore reads the verbatim rootful answer and pins
// the whole decoded store, including mount_program carried as its Executable out
// of podman's object. The same facts with the namespace flipped to rootless are
// the full named destination, so the decoder is exercised on real output even
// though a rootful launch does not name it.
func TestRealPodmanInfoDecodesToItsStore(t *testing.T) {
	f := factsFrom(t, realRootfulInfo(t))
	if !f.StoreKnown {
		t.Fatalf("store not read: %s", f.Unknown)
	}
	wantSpec := "overlay@/var/lib/containers/storage+/run/containers/storage:overlay.mount_program=" +
		realFuseOverlayfs
	if got := f.Store.Spec(); got != wantSpec {
		t.Errorf("spec =\n  %q\nwant\n  %q", got, wantSpec)
	}
	if len(f.Store.Dropped) != 0 || f.Store.ConfigFile != "/etc/containers/storage.conf" {
		t.Errorf("dropped = %q, configFile = %q", f.Store.Dropped, f.Store.ConfigFile)
	}
	f.Rootless = RootlessYes
	if got, want := ContainersStorageDestFor(f, "r:t"), "containers-storage:["+wantSpec+"]r:t"; got != want {
		t.Errorf("dest =\n  %q\nwant\n  %q", got, want)
	}
}

// TestPodmansDriverOptionsTravelWithTheStore: an explicit store spec makes the
// copier's storage library drop every driver option its config file had, so the
// ones podman reports are carried in the spec's `:options` suffix. mount_program
// is the one podman renders as an object; its Executable is the value. The
// additional image stores come from podman's LIST, one `imagestore=` each, and not
// from the string twin podman also writes, which holds only the last of them. An
// option the suffix cannot spell (it splits on commas) is dropped, and NAMED.
func TestPodmansDriverOptionsTravelWithTheStore(t *testing.T) {
	// The two image-store keys are rendered exactly as podman 5's libpod/info.go
	// renders two configured additional image stores.
	info := `{"host":{"security":{"rootless":true}},"store":{
	  "graphDriverName":"overlay",
	  "graphRoot":"/home/u/.local/share/containers/storage",
	  "runRoot":"/run/user/1000/containers",
	  "graphOptions":{
	    "overlay.mount_program":{"Executable":"/usr/bin/fuse-overlayfs","Package":"fuse-overlayfs","Version":"1.13"},
	    "overlay.ignore_chown_errors":"true",
	    "overlay.mountopt":"nodev,metacopy=on",
	    "overlay.additionalImageStores":["/a","/b"],
	    "overlay.imagestore":"/b"
	  }}}`
	f := factsFrom(t, info)
	if !f.StoreKnown {
		t.Fatalf("store not read: %s", f.Unknown)
	}
	want := "containers-storage:[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers" +
		":overlay.imagestore=/a,overlay.imagestore=/b,overlay.ignore_chown_errors=true," +
		"overlay.mount_program=/usr/bin/fuse-overlayfs]r:t"
	if got := ContainersStorageDestFor(f, "r:t"); got != want {
		t.Errorf("dest =\n  %q\nwant\n  %q", got, want)
	}
	if got := strings.Join(f.Store.Dropped, ","); got != "overlay.mountopt" {
		t.Errorf("dropped = %q, want only the option the suffix cannot spell", got)
	}
	if note := StoreWriteNote(f); !strings.Contains(note, "not carried: overlay.mountopt.") {
		t.Errorf("the launch line does not name the dropped option: %q", note)
	}
	if pf := StorePreflight(f); !strings.Contains(pf.Line, "not carried: overlay.mountopt.") {
		t.Errorf("`yolo check` does not name the dropped option the launch names: %q", pf.Line)
	}
	// A lone string twin (a podman that renders no list) is still carried.
	lone := factsFrom(t, `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay",`+
		`"graphRoot":"/g","runRoot":"/r","graphOptions":{"overlay.imagestore":"/a"}}}`)
	if got := lone.Store.Spec(); got != "overlay@/g+/r:overlay.imagestore=/a" {
		t.Errorf("a lone imagestore key: spec = %q", got)
	}
}

// TestAStoreThatCannotBeReadKeepsTodaysDestination is the tri-state rule: a
// store yolo could not read, or could not spell, is never guessed. The
// destination falls back to the bare `containers-storage:<ref>` it always was,
// and the launch line says so.
func TestAStoreThatCannotBeReadKeepsTodaysDestination(t *testing.T) {
	const ref = "localhost/yolo-jail:beef"
	for _, tc := range []struct {
		name, stdout string
		ok           bool
	}{
		{"podman would not run", ``, false},
		{"not json", `Error: unable to connect to Podman socket`, true},
		{"no store section", `{"host":{"security":{"rootless":true}}}`, true},
		{"no runRoot", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay","graphRoot":"/g"}}`, true},
		{"no driver", `{"host":{"security":{"rootless":true}},"store":{"graphRoot":"/g","runRoot":"/r"}}`, true},
		{"a relative graphRoot", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay","graphRoot":"g","runRoot":"/r"}}`, true},
		{"a colon in a root", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay","graphRoot":"/g:x","runRoot":"/r"}}`, true},
		{"a plus in graphRoot", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay","graphRoot":"/g+x","runRoot":"/r"}}`, true},
		{"a bracket in runRoot", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"overlay","graphRoot":"/g","runRoot":"/r]x"}}`, true},
		{"an at in the driver", `{"host":{"security":{"rootless":true}},"store":{"graphDriverName":"over@lay","graphRoot":"/g","runRoot":"/r"}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := ReadPodmanStoreFacts("podman", func([]string) (string, bool) { return tc.stdout, tc.ok })
			if f.StoreKnown {
				t.Fatalf("a store was claimed from %q: %+v", tc.stdout, f.Store)
			}
			if f.Unknown == "" {
				t.Error("no reason recorded for the unknown store")
			}
			if got := ContainersStorageDestFor(f, ref); got != ContainersStorageDest(ref) {
				t.Errorf("dest = %q, want today's %q", got, ContainersStorageDest(ref))
			}
			if note := StoreWriteNote(f); !strings.Contains(note, "storage.conf lookup") {
				t.Errorf("the launch line does not say the copier picks its own store: %q", note)
			}
		})
	}
	if f := ReadPodmanStoreFacts("podman", nil); f.StoreKnown || f.Rootless != RootlessUnknown {
		t.Errorf("with no way to ask, facts = %+v, want nothing known", f)
	}
}

// TestTheDefaultSeamReadsPodmanInfoOnceAndCopiesIntoItsStore pins fill()'s
// DEFAULT StoreFacts seam, which the table tests above inject around: with
// StoreFacts left nil, AutoLoadImage must run a real `podman info` (a fake podman
// on PATH answers it, rootless) exactly once, and the store it reports must reach
// the copy with the unshare prefix.
func TestTheDefaultSeamReadsPodmanInfoOnceAndCopiesIntoItsStore(t *testing.T) {
	withBuildDir(t)
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(dir, "podman-info-calls")
	writeScript(t, filepath.Join(bin, "podman"),
		`[ "$1" = info ] || exit 1`+"\nprintf x >> "+shWord(calls)+"\ncat <<'EOF'\n"+rootlessInfo+"\nEOF")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	storePath := storeManifest(t, "conf-image")
	f := newFakeRuntime()
	var out bytes.Buffer
	opts := c2Opts("podman", storePath, f, &out)
	opts.StoreFacts = nil // the REAL `podman info` read, answered by the fake podman

	if res := AutoLoadImage(opts); !res.OK {
		t.Fatalf("the launch failed: %s", out.String())
	}
	if n := fileSize(t, calls); n != 1 {
		t.Errorf("the delivery asked `podman info` %d times, want one read", n)
	}
	if len(f.copiedDests) != 1 {
		t.Fatalf("copies = %q, want one", f.copiedDests)
	}
	argv := f.copiedPrefixes[0] + " " + f.copiedDests[0]
	want := "podman unshare -- containers-storage:[overlay@/home/u/.local/share/containers/storage" +
		"+/run/user/1000/containers]" + JailImageRef("podman", storePath)
	if argv != want {
		t.Errorf("copy =\n  %q\nwant\n  %q", argv, want)
	}
}

// TestTheLaunchCopiesIntoTheStorePodmanReports PINS THE CALL SITE. The pure
// helpers above stay green if AutoLoadImage stops passing the facts' store to the
// copy; this does not. Every mode of the one production containers-storage write
// is driven through AutoLoadImage and the destination the copy actually received
// is compared. (`yolo capture` has no copier of its own: it runs the ordinary run
// pipeline, whose AutoLoadOptions leave this seam at its default —
// internal/cli/run's TestTheRunPathLeavesTheStoreReadToTheImageLoad.)
func TestTheLaunchCopiesIntoTheStorePodmanReports(t *testing.T) {
	for _, tc := range []struct {
		name, info, wantPrefix, wantStore, wantSays string
	}{
		{"rootless", rootlessInfo, "podman unshare --",
			"[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers]",
			"overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers"},
		{"rootful", realRootfulInfo(t), "", "", "podman is rootful"},
		{"rootless, unreadable store", `{"host":{"security":{"rootless":true}}}`, "podman unshare --", "",
			"could not read podman's store"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBuildDir(t)
			storePath := storeManifest(t, "site-image")
			f := newFakeRuntime()
			var out bytes.Buffer
			opts := c2Opts("podman", storePath, f, &out)
			facts := factsFrom(t, tc.info)
			asked := 0
			opts.StoreFacts = func() PodmanStoreFacts { asked++; return facts }

			if res := AutoLoadImage(opts); !res.OK {
				t.Fatalf("the launch failed: %s", out.String())
			}
			if asked != 1 {
				t.Errorf("store facts asked %d times, want one read per delivery", asked)
			}
			ref := JailImageRef("podman", storePath)
			want := "containers-storage:" + tc.wantStore + ref
			if len(f.copiedDests) != 1 || f.copiedDests[0] != want {
				t.Errorf("copied dests = %q, want [%q]", f.copiedDests, want)
			}
			if got := strings.Join(f.copiedPrefixes, "|"); got != tc.wantPrefix {
				t.Errorf("prefix = %q, want %q", got, tc.wantPrefix)
			}
			// The fake's store is keyed by the REF, so the launch finding its image
			// afterwards is also the proof that the spec did not leak into the name.
			if f.present[ref] == "" {
				t.Errorf("no image under %q after the copy; present = %v", ref, f.present)
			}
			if !strings.Contains(out.String(), tc.wantSays) {
				t.Errorf("the launch line does not say %q:\n%s", tc.wantSays, out.String())
			}
		})
	}
}

// TestTheArchiveArmsNeverAskForAStore: macOS podman and Apple Container deliver an
// OCI layout the runtime's own loader reads, so no local containers-storage is
// written and no store is consulted. A store spec leaking onto that path would be
// a destination skopeo rejects for the `oci:` transport.
func TestTheArchiveArmsNeverAskForAStore(t *testing.T) {
	for _, tc := range []struct {
		name, runtime string
		macOS         bool
	}{
		{"apple container", "container", false},
		{"podman on macOS", "podman", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withBuildDir(t)
			storePath := storeManifest(t, "archive-store-image")
			f := newFakeRuntime()
			var out bytes.Buffer
			opts := acOpts(storePath, f, &out)
			opts.Runtime = tc.runtime
			f.runtime = tc.runtime
			opts.IsMacOS = tc.macOS
			asked := 0
			opts.StoreFacts = func() PodmanStoreFacts { asked++; return factsFrom(t, rootlessInfo) }

			if res := AutoLoadImage(opts); !res.OK {
				t.Fatalf("the launch failed: %s", out.String())
			}
			if asked != 0 {
				t.Errorf("the archive arm asked for podman's store %d time(s)", asked)
			}
			for _, d := range f.copiedDests {
				if !strings.HasPrefix(d, "oci:") || strings.Contains(d, "[") {
					t.Errorf("archive dest = %q, want a plain oci: layout", d)
				}
			}
		})
	}
}

// TestDeliveryCopyArgvForIsTheLaunchsArgv: the exported composition other callers
// (the integration harness's own load, `yolo internal image-copy`, the macOS
// in-VM copier experiment) use is the prefix and destination the launch computes
// from the same facts.
func TestDeliveryCopyArgvForIsTheLaunchsArgv(t *testing.T) {
	for _, info := range []string{rootlessInfo, realRootfulInfo(t), `{}`} {
		f := factsFrom(t, info)
		got := DeliveryCopyArgvFor("podman", f, "/c/skopeo", "/i.json", "localhost/yolo-jail:x")
		dest, prefix := storeWrite("podman", f, "localhost/yolo-jail:x")
		want := copyArgv(prefix, "/c/skopeo", "/i.json", dest)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("DeliveryCopyArgvFor =\n  %q\nwant\n  %q", got, want)
		}
	}
}

// TestStorePreflightNamesTheStoreALaunchWillWrite is `yolo check`'s half: the
// store a launch will copy into, before any launch runs — and a WARNING when a
// rootless podman answered but its store could not be named, because that launch
// will let the copier choose, which is issue #47's failure. A rootful podman gets
// an ok line saying the copier resolves the system store; silence when podman did
// not answer at all, the tri-state rule the delivery-route line follows.
func TestStorePreflightNamesTheStoreALaunchWillWrite(t *testing.T) {
	known := StorePreflight(factsFrom(t, rootlessInfo))
	if known.Warn || !strings.Contains(known.Line,
		"overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers") {
		t.Errorf("known store = %+v, want an ok line naming podman's store", known)
	}
	if !strings.Contains(known.Line, "/usr/share/containers/storage.conf") {
		t.Errorf("the line does not say which storage.conf podman read: %q", known.Line)
	}
	unnamed := StorePreflight(factsFrom(t, `{"host":{"security":{"rootless":true}}}`))
	if !unnamed.Warn || !strings.Contains(unnamed.Hint, "storage.conf") {
		t.Errorf("unnamed store = %+v, want a warning with the storage.conf remedy", unnamed)
	}
	rootful := StorePreflight(factsFrom(t, realRootfulInfo(t)))
	if rootful.Warn || !strings.Contains(rootful.Line, "rootful") {
		t.Errorf("rootful = %+v, want an ok line saying the copier resolves the system store", rootful)
	}
	if silent := StorePreflight(ReadPodmanStoreFacts("podman", nil)); silent.Line != "" {
		t.Errorf("no answer produced a line: %+v", silent)
	}
}

// containersStorageRef is the image ref a `containers-storage:` destination
// names, as the transport parses it: an optional `[store]` spec
// (ContainersStorageDestFor) comes off the front and is not part of the name.
// The package's fake runtimes key their stores by it.
func containersStorageRef(dest string) string {
	ref := strings.TrimPrefix(dest, "containers-storage:")
	if strings.HasPrefix(ref, "[") {
		if i := strings.IndexByte(ref, ']'); i >= 0 {
			ref = ref[i+1:]
		}
	}
	return ref
}
