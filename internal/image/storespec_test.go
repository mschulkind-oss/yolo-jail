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
// `mkdir /run/containers: permission denied`. The copier now writes the store
// podman itself reports, named explicitly on the destination.

// rootlessInfo and rootfulInfo are `podman info --format json` as the two modes
// print it, trimmed to the keys the delivery reads plus a neighbor or two, so a
// decoder that matched the first "graphRoot"-shaped string anywhere would be
// caught. The rootless one is the reporter's host: podman read the distro's
// /usr/share file and still reported ROOTLESS paths.
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

const rootfulInfo = `{
  "host": {"security": {"rootless": false}},
  "store": {
    "configFile": "/etc/containers/storage.conf",
    "graphDriverName": "overlay",
    "graphRoot": "/var/lib/containers/storage",
    "runRoot": "/run/containers/storage"
  }
}`

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

// TestTheDestinationNamesTheStorePodmanReports is the fix as a table: the store
// in the destination is podman's own, for BOTH modes, so neither depends on the
// copier's own config lookup agreeing with podman's.
func TestTheDestinationNamesTheStorePodmanReports(t *testing.T) {
	const ref = "localhost/yolo-jail:0123456789abcdef"
	for _, tc := range []struct {
		name, info, want string
		rootless         PodmanRootless
	}{
		{"rootless", rootlessInfo,
			"containers-storage:[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers]" + ref,
			RootlessYes},
		{"rootful", rootfulInfo,
			"containers-storage:[overlay@/var/lib/containers/storage+/run/containers/storage]" + ref,
			RootlessNo},
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

// TestPodmansDriverOptionsTravelWithTheStore: an explicit store spec makes the
// copier's storage library drop every driver option its config file had, so the
// ones podman reports are carried in the spec's `:options` suffix. mount_program
// is the one podman renders as an object; its Executable is the value. An option
// the suffix cannot spell (it splits on commas) is dropped, and NAMED.
func TestPodmansDriverOptionsTravelWithTheStore(t *testing.T) {
	info := `{"host":{"security":{"rootless":true}},"store":{
	  "graphDriverName":"overlay",
	  "graphRoot":"/home/u/.local/share/containers/storage",
	  "runRoot":"/run/user/1000/containers",
	  "graphOptions":{
	    "overlay.mount_program":{"Executable":"/usr/bin/fuse-overlayfs","Package":"fuse-overlayfs","Version":"1.13"},
	    "overlay.ignore_chown_errors":"true",
	    "overlay.mountopt":"nodev,metacopy=on",
	    "overlay.additionalImageStores":["/a","/b"]
	  }}}`
	f := factsFrom(t, info)
	if !f.StoreKnown {
		t.Fatalf("store not read: %s", f.Unknown)
	}
	want := "containers-storage:[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers" +
		":overlay.ignore_chown_errors=true,overlay.mount_program=/usr/bin/fuse-overlayfs]r:t"
	if got := ContainersStorageDestFor(f, "r:t"); got != want {
		t.Errorf("dest =\n  %q\nwant\n  %q", got, want)
	}
	if got := strings.Join(f.Store.Dropped, ","); got != "overlay.additionalImageStores,overlay.mountopt" {
		t.Errorf("dropped = %q, want the two options the suffix cannot spell", got)
	}
	if note := StoreWriteNote(f); !strings.Contains(note, "overlay.mountopt") {
		t.Errorf("the launch line does not name a dropped option: %q", note)
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
		{"no runRoot", `{"store":{"graphDriverName":"overlay","graphRoot":"/g"}}`, true},
		{"no driver", `{"store":{"graphRoot":"/g","runRoot":"/r"}}`, true},
		{"a relative graphRoot", `{"store":{"graphDriverName":"overlay","graphRoot":"g","runRoot":"/r"}}`, true},
		{"a colon in a root", `{"store":{"graphDriverName":"overlay","graphRoot":"/g:x","runRoot":"/r"}}`, true},
		{"a plus in graphRoot", `{"store":{"graphDriverName":"overlay","graphRoot":"/g+x","runRoot":"/r"}}`, true},
		{"a bracket in runRoot", `{"store":{"graphDriverName":"overlay","graphRoot":"/g","runRoot":"/r]x"}}`, true},
		{"an at in the driver", `{"store":{"graphDriverName":"over@lay","graphRoot":"/g","runRoot":"/r"}}`, true},
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
			if note := StoreWriteNote(f); !strings.Contains(note, "storage.conf") {
				t.Errorf("the launch line does not say the copier picks its own store: %q", note)
			}
		})
	}
	if f := ReadPodmanStoreFacts("podman", nil); f.StoreKnown || f.Rootless != RootlessUnknown {
		t.Errorf("with no way to ask, facts = %+v, want nothing known", f)
	}
}

// TestTheCopierArgvDoesNotDependOnAStorageConf reproduces the reporter's host as
// far as a unit test can. The REAL default seam runs a fake `podman` on PATH that
// answers `podman info` the way podman 5.7.0 did there (rootless paths, read from
// the distro file), while the process environment carries each storage.conf
// arrangement the copier's storage library would consult — including the
// reporter's, a root-path file and no user file. The argv handed to the copier
// must be identical under all of them, must name podman's store, and must never
// name the root runroot the copier resolved for itself.
func TestTheCopierArgvDoesNotDependOnAStorageConf(t *testing.T) {
	rootConf := "[storage]\ndriver = \"overlay\"\n" +
		"runroot = \"/run/containers/storage\"\ngraphroot = \"/var/lib/containers/storage\"\n"
	userConf := "[storage]\ndriver = \"vfs\"\nrunroot = \"/tmp/elsewhere/run\"\ngraphroot = \"/tmp/elsewhere/g\"\n"
	for _, arrangement := range []struct {
		name string
		env  func(t *testing.T, dir string)
	}{
		{"no storage.conf of the user's own, a root-path system file (issue #47)", func(t *testing.T, dir string) {
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty-xdg"))
			t.Setenv("CONTAINERS_STORAGE_CONF", writeFile(t, filepath.Join(dir, "root.conf"), rootConf))
		}},
		{"a user storage.conf naming another store", func(t *testing.T, dir string) {
			xdg := filepath.Join(dir, "xdg")
			writeFile(t, filepath.Join(xdg, "containers", "storage.conf"), userConf)
			t.Setenv("XDG_CONFIG_HOME", xdg)
			t.Setenv("CONTAINERS_STORAGE_CONF", "")
		}},
		{"nothing at all", func(t *testing.T, dir string) {
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "none"))
			t.Setenv("CONTAINERS_STORAGE_CONF", "")
		}},
	} {
		t.Run(arrangement.name, func(t *testing.T) {
			withBuildDir(t)
			dir := t.TempDir()
			arrangement.env(t, dir)
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			calls := filepath.Join(dir, "podman-info-calls")
			writeScript(t, filepath.Join(bin, "podman"),
				`[ "$1" = info ] || exit 1`+"\nprintf x >> "+calls+"\ncat <<'EOF'\n"+rootlessInfo+"\nEOF")
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
			if strings.Contains(argv, "/run/containers") || strings.Contains(argv, "/var/lib/containers") {
				t.Errorf("the copy names the ROOT store: %q", argv)
			}
		})
	}
}

// TestTheLaunchCopiesIntoTheStorePodmanReports PINS THE CALL SITE. The pure
// helpers above stay green if AutoLoadImage stops passing the facts' store to the
// copy; this does not. Every mode of the one production containers-storage write
// is driven through AutoLoadImage and the destination the copy actually received
// is compared. (`yolo capture` has no copier of its own: it runs the ordinary run
// pipeline, which reaches this same site.)
func TestTheLaunchCopiesIntoTheStorePodmanReports(t *testing.T) {
	for _, tc := range []struct {
		name, info, wantPrefix, wantStore string
	}{
		{"rootless", rootlessInfo, "podman unshare --",
			"[overlay@/home/u/.local/share/containers/storage+/run/user/1000/containers]"},
		{"rootful", rootfulInfo, "",
			"[overlay@/var/lib/containers/storage+/run/containers/storage]"},
		{"unreadable store", `{"host":{"security":{"rootless":false}}}`, "", ""},
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
			if tc.wantStore != "" && !strings.Contains(out.String(), strings.Trim(tc.wantStore, "[]")) {
				t.Errorf("the launch line does not name the store it wrote:\n%s", out.String())
			}
			if tc.wantStore == "" && !strings.Contains(out.String(), "could not read podman's store") {
				t.Errorf("the launch line does not say the store was unknown:\n%s", out.String())
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
// (the integration harness's own load, the macOS in-VM copier experiment) use is
// the prefix and destination the launch computes from the same facts.
func TestDeliveryCopyArgvForIsTheLaunchsArgv(t *testing.T) {
	for _, info := range []string{rootlessInfo, rootfulInfo, `{}`} {
		f := factsFrom(t, info)
		got := DeliveryCopyArgvFor("podman", f, "/c/skopeo", "/i.json", "localhost/yolo-jail:x")
		want := copyArgv(StoreWritePrefix("podman", f.Rootless), "/c/skopeo", "/i.json",
			ContainersStorageDestFor(f, "localhost/yolo-jail:x"))
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("DeliveryCopyArgvFor =\n  %q\nwant\n  %q", got, want)
		}
	}
}

// TestStorePreflightNamesTheStoreALaunchWillWrite is `yolo check`'s half: the
// store a launch will copy into, before any launch runs — and a WARNING when
// podman answered but its store could not be named, because that launch will let
// the copier choose, which is issue #47's failure. Silence when podman did not
// answer at all, the tri-state rule the delivery-route line follows.
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

// writeFile writes body at path, creating parents, and returns the path.
func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
