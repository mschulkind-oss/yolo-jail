package config

// mounts_test.go pins the `mounts` key's read-write form (docs/design/context-mounts.md
// §2.1–§2.3, §4 step 1) at its CALL SITES: every rule is asserted through ValidateConfig,
// which is what both `yolo check` and the launch's preflight run, or through LoadRWMounts,
// which is what the run pipeline mounts from. A test of ParseMountElement alone would pass
// with validateMounts' calls to it deleted.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// mountsHost is a fixture host: a resolved HOME (the darwin t.TempDir symlink class,
// resolved where it is minted), an empty user config dir, a workspace, and YOLO_VERSION
// cleared so the host-only halves run.
type mountsHost struct {
	home, ws string
}

func newMountsHost(t *testing.T) mountsHost {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	// Two levels down, so the workspace's parent contains the workspace and NOT the home:
	// every t.TempDir() of one test shares a root, and a mount of that root would trip
	// the home clause before the workspace one.
	wsRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(wsRoot, "proj", "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	return mountsHost{home: home, ws: ws}
}

// user writes the user config.
func (h mountsHost) user(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.home, ".config", "yolo-jail", "config.jsonc"),
		[]byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// workspace writes yolo-jail.jsonc.
func (h mountsHost) workspace(t *testing.T, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.ws, "yolo-jail.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// validate loads the merged config the way the launch does and validates it.
func (h mountsHost) validate(t *testing.T) (errs, warns []string) {
	t.Helper()
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return ValidateConfig(cfg, h.ws, nil)
}

// dir makes a directory outside the home and the workspace and returns its resolved path.
func mountSourceDir(t *testing.T, name string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func joined(l []string) string { return strings.Join(l, "\n") }

// THE SHAPE (§2.1): the string form parses as it always did, the object form carries a
// required mode and an optional `at`, and every malformed spelling is a `yolo check` error
// naming the element — the `:rw` suffix among them, refused with the object form it should
// have been.
func TestMountsValidationRefusesEveryMalformedElement(t *testing.T) {
	src := mountSourceDir(t, "data")
	for _, tc := range []struct {
		name, element, want string
	}{
		{"a :rw string suffix", `"` + src + `:rw"`, `{"host": "` + src + `", "mode": "rw"}`},
		{"a :rw suffix after a destination", `"` + src + `:/ctx/d:rw"`, `is not a mode`},
		{"an object with no mode", `{"host": "` + src + `"}`, `"mode" is required`},
		{"an unknown mode", `{"host": "` + src + `", "mode": "write"}`, `must be "ro" or "rw"`},
		{"a typo'd key", `{"host": "` + src + `", "mode": "ro", "into": "/ctx/x"}`, `unknown key "into"`},
		{"an object with no host", `{"mode": "ro"}`, `"host" is required`},
		{"a relative at", `{"host": "` + src + `", "mode": "ro", "at": "ctx/x"}`, "must be absolute"},
		{"an rw at outside /ctx", `{"host": "` + src + `", "mode": "rw", "at": "/home/agent/x"}`, "must land under /ctx/"},
		{"an rw at of /ctx itself", `{"host": "` + src + `", "mode": "rw", "at": "/ctx"}`, "must land under /ctx/"},
		{"a colon in the host path", `{"host": "/a:b", "mode": "ro"}`, "contains a colon"},
		{"a number", `7`, "expected a string or an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			h.user(t, `{"mounts": [`+tc.element+`]}`)
			errs, _ := h.validate(t)
			if !strings.Contains(joined(errs), tc.want) || !strings.Contains(joined(errs), "config.mounts[0]") {
				t.Errorf("errors = %q, want one naming config.mounts[0] and containing %q", errs, tc.want)
			}
		})
	}
}

// THE `:rw` REFUSAL'S SUGGESTION IS ITSELF A VALID ENTRY. The docker-style spelling a user
// is most likely to write, "host:/ctx/d:rw", used to be answered with an object whose
// `host` was "host:/ctx/d" — which the object form refuses for its colon, so following the
// message produced a second error. The suggestion splits the destination into `at`, and
// pasting it into the config validates clean and keeps the destination the user wrote.
func TestTheRWSuffixRefusalSuggestsAnObjectThatValidates(t *testing.T) {
	src := mountSourceDir(t, "data")
	for _, tc := range []struct{ name, element, want string }{
		{"with a destination", src + ":/ctx/d:rw", `{"host": "` + src + `", "at": "/ctx/d", "mode": "rw"}`},
		{"bare", src + ":rw", `{"host": "` + src + `", "mode": "rw"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			h.user(t, `{"mounts": ["`+tc.element+`"]}`)
			errs, _ := h.validate(t)
			if got := joined(errs); !strings.Contains(got, tc.want) {
				t.Fatalf("errors = %q, want the suggestion %s", errs, tc.want)
			}
			h.user(t, `{"mounts": [`+tc.want+`]}`)
			if errs, _ := h.validate(t); len(errs) != 0 {
				t.Fatalf("the suggested object %s does not validate: %q", tc.want, errs)
			}
		})
	}
}

// The control for the table above: both forms, in both modes, validate clean.
func TestMountsValidationAcceptsBothForms(t *testing.T) {
	h := newMountsHost(t)
	a, b, c := mountSourceDir(t, "lib"), mountSourceDir(t, "data"), mountSourceDir(t, "notes")
	h.user(t, `{"mounts": ["`+a+`", "`+a+`:/opt/lib",
	  {"host": "`+b+`", "at": "/ctx/datasets", "mode": "rw"},
	  {"host": "`+c+`", "mode": "ro"}]}`)
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("a well-formed mounts list failed validation: %q", errs)
	}
}

// THE TRUST PREDICATE'S REFUSAL HALF (§2.2, OQ-WT1's rule D until it rules): a read-write
// element in the workspace config — committed or local — fails validation, which is also
// the launch's refusal. The same element in the user config passes, and a read-only object
// element in the workspace config keeps today's scoping.
func TestAReadWriteMountInTheWorkspaceConfigIsRefused(t *testing.T) {
	src := mountSourceDir(t, "data")
	rw := `{"host": "` + src + `", "mode": "rw"}`
	for _, file := range []string{"yolo-jail.jsonc", "yolo-jail.local.jsonc"} {
		t.Run(file, func(t *testing.T) {
			h := newMountsHost(t)
			if err := os.WriteFile(filepath.Join(h.ws, file), []byte(`{"mounts": [`+rw+`]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			errs, _ := h.validate(t)
			got := joined(errs)
			if !strings.Contains(got, "user-scope only") || !strings.Contains(got, paths.UserConfigPath()) {
				t.Fatalf("a read-write mount in %s was not refused as workspace scope: %q", file, errs)
			}
		})
	}
	t.Run("the same element in the user config", func(t *testing.T) {
		h := newMountsHost(t)
		h.user(t, `{"mounts": [`+rw+`]}`)
		if errs, _ := h.validate(t); len(errs) != 0 {
			t.Fatalf("a user-scope read-write mount was refused: %q", errs)
		}
	})
	t.Run("a read-only object in the workspace config", func(t *testing.T) {
		h := newMountsHost(t)
		h.workspace(t, `{"mounts": [{"host": "`+src+`", "mode": "ro"}]}`)
		if errs, _ := h.validate(t); len(errs) != 0 {
			t.Fatalf("a read-only workspace mount was refused: %q", errs)
		}
	})
}

// THE TRUST PREDICATE'S ADMIT HALF, which is the boundary: LoadRWMounts — what the run
// pipeline mounts writable — reads the user scope directly, so a workspace element never
// reaches it even when validation is bypassed, and a read-only element is not its business.
func TestLoadRWMountsReadsTheUserScopeAlone(t *testing.T) {
	h := newMountsHost(t)
	mine, theirs, ro := mountSourceDir(t, "mine"), mountSourceDir(t, "theirs"), mountSourceDir(t, "ro")
	h.user(t, `{"mounts": [{"host": "`+mine+`", "mode": "rw", "at": "/ctx/mine"}, "`+ro+`"]}`)
	h.workspace(t, `{"mounts": [{"host": "`+theirs+`", "mode": "rw"}]}`)

	got, err := LoadRWMounts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != mine || got[0].At != "/ctx/mine" || !got[0].RW {
		t.Fatalf("LoadRWMounts = %+v, want exactly the user config's one read-write element", got)
	}
}

// A --user-layer is user scope (workspace-config-trust.md §4.1's table), so its read-write
// element is honored like the user config's own.
func TestLoadRWMountsHonorsAUserLayer(t *testing.T) {
	h := newMountsHost(t)
	src := mountSourceDir(t, "layered")
	layer := filepath.Join(t.TempDir(), "layer.jsonc")
	if err := os.WriteFile(layer, []byte(`{"mounts": [{"host": "`+src+`", "mode": "rw"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(UserLayerEnv, layer)
	_ = h
	got, err := LoadRWMounts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Host != src {
		t.Fatalf("LoadRWMounts = %+v, want the user layer's element", got)
	}
}

// THE REFUSAL SET (§2.3), on resolved paths, through ValidateConfig. Clause 1 is the SHARED
// credential-boundary predicate (paths.WorkspaceScopeBreach), so the home, both yolo dirs
// and anything inside those two refuse — the approvals directory among them, which is what
// keeps BB-D34's "no mount can write an approval" true. Clause 2 is workspace overlap in
// either direction. ~/.ssh is deliberately not named (CX-D2), so it passes like any other
// directory under the home.
func TestTheReadWriteRefusalSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		host func(h mountsHost) string
		want string // "" = accepted
	}{
		{"the home itself", func(h mountsHost) string { return h.home }, "IS your home directory"},
		{"a directory containing the home", func(h mountsHost) string { return filepath.Dir(h.home) }, "CONTAINS your home directory"},
		{"yolo's state dir", func(h mountsHost) string { return filepath.Join(h.home, ".local", "share", "yolo-jail") }, "yolo's own state directory"},
		{"the approvals dir", func(h mountsHost) string { return paths.ApprovalsDir() }, "is INSIDE yolo's own state directory"},
		// The capture-store exemption is a WORKSPACE's (a `yolo capture` jail works in its own
		// scratch tree there); a writable mount of the store is every other workspace's
		// installers, rewritable from one jail — the cross-jail injection capturesArgs refuses
		// to hand out even on a backend that cannot honor :ro.
		{"the capture store's entries", func(h mountsHost) string { return filepath.Join(paths.CapturesDir(), "entries") }, "is INSIDE yolo's own state directory"},
		{"a capture staging dir", func(h mountsHost) string { return filepath.Join(paths.CapturesDir(), "staging", "x") }, "is INSIDE yolo's own state directory"},
		{"yolo's user config dir", func(h mountsHost) string { return filepath.Join(h.home, ".config", "yolo-jail") }, "yolo's user config directory"},
		{"the workspace itself", func(h mountsHost) string { return h.ws }, "is inside the workspace"},
		{"a directory inside the workspace", func(h mountsHost) string { return filepath.Join(h.ws, "sub") }, "is inside the workspace"},
		{"a directory containing the workspace", func(h mountsHost) string { return filepath.Dir(h.ws) }, "contains the workspace"},
		{"~/.ssh (CX-D2: no named list)", func(h mountsHost) string { return filepath.Join(h.home, ".ssh") }, ""},
		{"an ordinary project dir", func(h mountsHost) string { return filepath.Join(h.home, "scratch") }, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			host := tc.host(h)
			if err := os.MkdirAll(host, 0o755); err != nil {
				t.Fatal(err)
			}
			h.user(t, `{"mounts": [{"host": "`+host+`", "mode": "rw"}]}`)
			errs, _ := h.validate(t)
			got := joined(errs)
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("an rw mount of %s was refused: %q", host, errs)
				}
				return
			}
			if !strings.Contains(got, "is refused") || !strings.Contains(got, tc.want) {
				t.Fatalf("an rw mount of %s: errors %q, want a refusal containing %q", host, errs, tc.want)
			}
		})
	}
}

// The ro form keeps today's rules (OQ-CX3, answered by OQ-PR3): a read-only mount of yolo's
// logs dir — this repo's own `mounts` entry — is not refused by the predicate the rw form
// runs.
func TestTheReadOnlyFormGetsNoShareOfTheRefusalSet(t *testing.T) {
	h := newMountsHost(t)
	logs := filepath.Join(h.home, ".local", "share", "yolo-jail", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	h.user(t, `{"mounts": ["`+logs+`", {"host": "`+h.home+`", "mode": "ro"}]}`)
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("a read-only mount was refused by the read-write refusal set: %q", errs)
	}
}

// Two context mounts at one jail path are a `yolo check` error (§2.1): today a collision is
// whichever bind podman applied last.
func TestTwoMountsAtOneJailPathAreRefused(t *testing.T) {
	h := newMountsHost(t)
	a := mountSourceDir(t, "lib")
	b := mountSourceDir(t, "lib") // same basename, different dir → both default to /ctx/lib
	h.user(t, `{"mounts": ["`+a+`", {"host": "`+b+`", "mode": "rw"}]}`)
	errs, _ := h.validate(t)
	if got := joined(errs); !strings.Contains(got, "both land at /ctx/lib") {
		t.Fatalf("errors = %q, want the /ctx/lib collision", errs)
	}
}

// ONLY A MOUNT THE LAUNCH WOULD BIND CAN COLLIDE. An element whose source does not exist
// is skipped with a warning at launch (§2.1), and so is a pack grant whose source is absent,
// so neither is ever bound. A config listing one path per machine — "~/src/lib" on one,
// "/opt/lib" on another, both /ctx/lib — launched on every machine before the duplicate
// check existed, and must keep launching: refusing it would refuse a jail podman would
// have started.
func TestAMountThatIsNeverBoundCollidesWithNothing(t *testing.T) {
	t.Run("two config elements, one source absent", func(t *testing.T) {
		h := newMountsHost(t)
		present := mountSourceDir(t, "lib")
		absent := filepath.Join(t.TempDir(), "elsewhere", "lib")
		h.user(t, `{"mounts": ["`+present+`", "`+absent+`"]}`)
		errs, warns := h.validate(t)
		if len(errs) != 0 {
			t.Fatalf("a collision with an element whose source is absent was refused: %q", errs)
		}
		if !strings.Contains(joined(warns), "does not exist and will be skipped: "+absent) {
			t.Errorf("the absent source lost its warning: %q", warns)
		}
	})
	t.Run("a pack grant whose source is absent", func(t *testing.T) {
		h := newMountsHost(t)
		pack := mountPackDir(t, "acme", "datasets/acme", "acme")
		src := mountSourceDir(t, "mine")
		h.user(t, `{"packs": ["file://`+pack+`"], "mounts": ["`+src+`:/ctx/acme"]}`)
		if errs, _ := h.validate(t); len(errs) != 0 {
			t.Fatalf("a collision with a pack grant whose source is absent was refused: %q", errs)
		}
	})
}

// YOLO'S OWN /ctx CHILDREN SHARE THE NAMESPACE (§3.2: "it shares one namespace with the
// composed copies (host-user/…), just as /ctx does on podman. The collision rule is the
// `yolo check` duplicate-destination error"). A `mounts` element at one of them was a
// launch podman refused late ("duplicate mount destination"); one inside one had podman
// create its mountpoint in the tree yolo binds there; one containing them had podman
// create yolo's mountpoints inside the user's source — measured 2026-09-30 with podman 5.8.7
// in a nested (rootful) jail: the nested mountpoint is created in the host directory even
// when the parent bind is :ro and refuses the jail's own writes.
// Each is a `yolo check` error naming yolo's path, a bare element whose BASENAME is one of
// the names included — the likeliest way to hit it.
func TestAMountOnYolosOwnContextPathIsRefused(t *testing.T) {
	for _, tc := range []struct{ name, element, want string }{
		{"a bare element named packs", `"` + mountSourceDir(t, "packs") + `"`, "/ctx/packs"},
		{"an explicit at of the captures store", `{"host": "` + mountSourceDir(t, "x") + `", "mode": "rw", "at": "/ctx/captures"}`, "/ctx/captures"},
		{"host-user", `"` + mountSourceDir(t, "y") + `:/ctx/host-user"`, "/ctx/host-user"},
		{"host-nvim-config", `"` + mountSourceDir(t, "z") + `:/ctx/host-nvim-config"`, "/ctx/host-nvim-config"},
		{"inside the pack tree", `{"host": "` + mountSourceDir(t, "w") + `", "mode": "rw", "at": "/ctx/packs/extra"}`, "/ctx/packs"},
		{"the context dir itself", `"` + mountSourceDir(t, "v") + `:/ctx"`, "/ctx/packs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			h.user(t, `{"mounts": [`+tc.element+`]}`)
			errs, _ := h.validate(t)
			got := joined(errs)
			if !strings.Contains(got, "yolo binds") || !strings.Contains(got, tc.want) {
				t.Fatalf("errors = %q, want a refusal naming yolo's own %s", errs, tc.want)
			}
		})
	}
	// The control: a neighbour of a reserved name is an ordinary destination.
	h := newMountsHost(t)
	h.user(t, `{"mounts": ["`+mountSourceDir(t, "packs-lib")+`", "`+mountSourceDir(t, "u")+`:/ctx/host-users"]}`)
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("a destination beside a reserved name was refused: %q", errs)
	}
}

// mountPackDir writes a local pack declaring one `mount` of ~/<host> at /ctx/<into>.
func mountPackDir(t *testing.T, name, host, into string) string {
	t.Helper()
	pack := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"),
		[]byte(`{"name":"`+name+`","contributes":[{"kind":"mount","host":"`+host+`","into":"`+into+`"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	return pack
}

// THE FENCE SEES AN OBJECT ELEMENT (CX-D1's warning): mountHostSources reads through
// ParseMounts, so a read-write object element reaching the broker's credential path is
// disclosed from the user config and refused from a workspace one, like a string element.
func TestTheMountFenceJudgesAnObjectElement(t *testing.T) {
	h := newMountsHost(t)
	t.Setenv("GH_CONFIG_DIR", "")
	gh := filepath.Join(h.home, ".config", "gh")
	if err := os.MkdirAll(gh, 0o755); err != nil {
		t.Fatal(err)
	}
	obj := `{"host": "` + gh + `", "mode": "rw", "at": "/ctx/gh"}`
	h.user(t, `{"mounts": [`+obj+`]}`)
	cfg, err := LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	_, warns := ValidateConfig(cfg, h.ws, brokeredResolver)
	if got := joined(warns); !strings.Contains(got, "read AND write it") {
		t.Fatalf("the fence did not disclose a user-scope read-write object element: %q", warns)
	}

	h.user(t, `{}`)
	h.workspace(t, `{"mounts": [{"host": "`+gh+`", "mode": "ro", "at": "/ctx/gh"}]}`)
	cfg, err = LoadConfig(h.ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, h.ws, brokeredResolver)
	if got := joined(errs); !strings.Contains(got, "A workspace config may not mount it") {
		t.Fatalf("the fence skipped a workspace object element: %q", errs)
	}
}

// The other party to a collision is a SELECTED pack's `mount` at /ctx/<into>, which the
// config cannot see by reading itself: the check resolves the selection the launch stages.
func TestAMountAtAPackMountsJailPathIsRefused(t *testing.T) {
	h := newMountsHost(t)
	pack := mountPackDir(t, "acme", "datasets/acme", "acme")
	// The grant's source EXISTS, so the launch would bind it (an absent one collides with
	// nothing: TestAMountThatIsNeverBoundCollidesWithNothing).
	if err := os.MkdirAll(filepath.Join(h.home, "datasets", "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	src := mountSourceDir(t, "mine")
	h.user(t, `{"packs": ["file://`+pack+`"], "mounts": ["`+src+`:/ctx/acme"]}`)
	errs, _ := h.validate(t)
	if got := joined(errs); !strings.Contains(got, "pack acme's mount of ~/datasets/acme") ||
		!strings.Contains(got, "both land at /ctx/acme") {
		t.Fatalf("errors = %q, want the collision with pack acme's mount", errs)
	}
}
