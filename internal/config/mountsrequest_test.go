package config

// mountsrequest_test.go pins RC-D24 (docs/design/readable-config.md): a project's TOML config
// may REQUEST a writable mount with `write = true` once the user config's
// `[allow.mounts] on_request = ["write"]` allows it, and the config-change gate then approves it
// with the mount shown loud. Every rule is asserted at its call site: ValidateConfig (what
// `yolo check` and the launch preflight run), WorkspaceRead.WritableRequests and
// CheckWorkspaceMounts (what the gate reads and judges, and so what the run pipeline mounts),
// and ApproveConfigAndScope (the gate).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const allowMountsLines = "[allow.mounts]\non_request = [\"write\"]\n"

// gateRequests are the writable requests the config-change gate's own read of ws finds.
func gateRequests(t *testing.T, ws string) []WorkspaceMount {
	t.Helper()
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	return read.WritableRequests()
}

// userTOML writes the user config as config.toml.
func (h mountsHost) userTOML(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(h.home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// workspaceFile writes one workspace config file by name.
func (h mountsHost) workspaceFile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.ws, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeRequest(src string) string {
	return "mounts = [{ host = '" + src + "', at = '/ctx/data', write = true }]\n"
}

// (a) Without the user line, a TOML project's `write = true` is refused, and the refusal's next
// step is the exact two lines, the exact file to put them in, and the `yolo config add` that
// writes them (the verb creates the missing table and list, OQ-RC10).
func TestAWritableMountRequestWithoutTheAllowLineIsRefusedNamingIt(t *testing.T) {
	src := mountSourceDir(t, "data")
	for _, file := range []string{paths.WorkspaceConfigTOMLName, paths.WorkspaceLocalConfigTOMLName} {
		t.Run(file, func(t *testing.T) {
			h := newMountsHost(t)
			userPath := h.userTOML(t, "packs = []\n")
			h.workspaceFile(t, file, writeRequest(src))
			errs, _ := h.validate(t)
			got := joined(errs)
			for _, want := range []string{"write = true", userPath, "[allow.mounts]", `on_request = ["write"]`} {
				if !strings.Contains(got, want) {
					t.Fatalf("refusal %q does not name %q", got, want)
				}
			}
			if !strings.Contains(got, "yolo config add allow.mounts.on_request write --user") {
				t.Fatalf("refusal does not name the verb that creates the table: %q", got)
			}
			if refusals := CheckWorkspaceMounts(h.ws, gateRequests(t, h.ws), nil); len(refusals) == 0 {
				t.Fatal("the gate's check let a request through without the allow line")
			}
		})
	}
	t.Run("no user config file at all", func(t *testing.T) {
		h := newMountsHost(t)
		h.workspaceFile(t, paths.WorkspaceConfigTOMLName, writeRequest(src))
		errs, _ := h.validate(t)
		want := filepath.Join(h.home, ".config", "yolo-jail", paths.UserConfigTOMLName)
		if got := joined(errs); !strings.Contains(got, want) || !strings.Contains(got, "[allow.mounts]") {
			t.Fatalf("refusal %q does not name the TOML user config to create, %s", got, want)
		}
	})
}

// (b) With the user line the request validates, and the run pipeline's reader returns it
// read-write; a read-only element beside it is not the reader's business.
func TestAWritableMountRequestIsAcceptedOnceTheUserAllowsIt(t *testing.T) {
	src, ro := mountSourceDir(t, "data"), mountSourceDir(t, "ro")
	h := newMountsHost(t)
	h.userTOML(t, allowMountsLines)
	h.workspaceFile(t, paths.WorkspaceConfigTOMLName,
		"mounts = [{ host = '"+src+"', at = '/ctx/data', write = true }, { host = '"+ro+"', at = '/ctx/ro', mode = 'ro' }]\n")
	if errs, _ := h.validate(t); len(errs) != 0 {
		t.Fatalf("an allowed writable mount request was refused: %q", errs)
	}
	got := gateRequests(t, h.ws)
	if len(got) != 1 || got[0].Mount.Host != src || got[0].Mount.At != "/ctx/data" || !got[0].Mount.RW || got[0].Resolved != src {
		t.Fatalf("WritableRequests = %+v, want the one requested writable mount", got)
	}
	if refusals := CheckWorkspaceMounts(h.ws, got, nil); len(refusals) != 0 {
		t.Fatalf("the gate's check refused an allowed request: %q", refusals)
	}
	// The user scope's own reader is unchanged: it never returns a workspace element.
	if user, err := LoadRWMounts(nil); err != nil || len(user) != 0 {
		t.Fatalf("LoadRWMounts = %+v, %v; a workspace request leaked into the user-scope reader", user, err)
	}
}

// An allowed request is still a read-write mount: the refusal set (the credential boundary,
// overlap with the workspace) and the /ctx-only rule judge it exactly as they judge one from
// the user config. The allow line widens WHERE one may be written, never WHAT it may be.
func TestAnAllowedRequestStillMeetsTheRefusalSet(t *testing.T) {
	for _, tc := range []struct {
		name, element, want string
	}{
		{"the home", "{ host = '~', at = '/ctx/home', write = true }", "the read-write source"},
		{"inside the workspace", "{ host = '%WS%/sub', at = '/ctx/sub', write = true }", "inside the workspace"},
		{"outside /ctx", "{ host = '%SRC%', at = '/srv/data', write = true }", "must land under /ctx/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			src := mountSourceDir(t, "data")
			if err := os.MkdirAll(filepath.Join(h.ws, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			h.userTOML(t, allowMountsLines)
			el := strings.NewReplacer("%WS%", h.ws, "%SRC%", src).Replace(tc.element)
			h.workspaceFile(t, paths.WorkspaceConfigTOMLName, "mounts = ["+el+"]\n")
			if errs, _ := h.validate(t); !strings.Contains(joined(errs), tc.want) {
				t.Fatalf("errs = %q, want %q", errs, tc.want)
			}
		})
	}
}

// (c) The ceiling's own validation: `always` is refused for mounts, saying why, and
// `on_request` takes only "write".
func TestTheAllowMountsCeilingIsValidated(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"always", "[allow.mounts]\nalways = [\"write\"]\n", []string{"allow.mounts.always", "approved", "on_request"}},
		{"an unknown capability", "[allow.mounts]\non_request = [\"read\"]\n", []string{"allow.mounts.on_request", `"read"`, `"write"`}},
		{"an unknown key", "[allow.mounts]\nwritable = true\n", []string{"allow.mounts.writable"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newMountsHost(t)
			h.userTOML(t, tc.body)
			errs, _ := h.validate(t)
			got := joined(errs)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("errs %q do not contain %q", got, want)
				}
			}
		})
	}
	t.Run("the valid ceiling alone", func(t *testing.T) {
		h := newMountsHost(t)
		h.userTOML(t, allowMountsLines)
		if errs, _ := h.validate(t); len(errs) != 0 {
			t.Fatalf("the valid [allow.mounts] ceiling was refused: %q", errs)
		}
	})
}

// (d) Not backported: a read-write element in a JSONC workspace config keeps its refusal, the
// user's allow line notwithstanding, and the reader never returns it.
func TestAJSONCWorkspaceReadWriteMountIsStillRefusedWithTheAllowLine(t *testing.T) {
	src := mountSourceDir(t, "data")
	for _, el := range []string{`{"host": "` + src + `", "mode": "rw"}`, `{"host": "` + src + `", "write": true}`} {
		h := newMountsHost(t)
		h.userTOML(t, allowMountsLines)
		h.workspace(t, `{"mounts": [`+el+`]}`)
		errs, _ := h.validate(t)
		if got := joined(errs); !strings.Contains(got, "is still in the deprecated JSONC format, and a writable mount request") || !strings.Contains(got, "yolo config migrate") {
			t.Fatalf("JSONC workspace %s was not refused: %q", el, errs)
		}
		if rw := gateRequests(t, h.ws); len(rw) != 0 {
			t.Fatalf("the gate read a JSONC element as a request: %+v", rw)
		}
	}
}

// `write` and `mode` together are refused rather than one silently winning.
func TestWriteAndModeTogetherAreRefused(t *testing.T) {
	_, problems := ParseMountElement(decodeMountElement(t, `{"host": "/x", "mode": "ro", "write": true}`), "config.mounts[0]")
	if !strings.Contains(strings.Join(problems, "\n"), `"write"`) {
		t.Fatalf("problems = %q, want a write/mode conflict", problems)
	}
	m, problems := ParseMountElement(decodeMountElement(t, `{"host": "/x", "write": false}`), "config.mounts[0]")
	if len(problems) != 0 || m.RW {
		t.Fatalf("write = false: %+v %q, want a read-only element", m, problems)
	}
}

func decodeMountElement(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// (e, the gate's half) A changed config that holds a writable mount hands the prompter the
// mount, so it can be shown loud; one that holds none hands it nothing. --accept-config-changes
// with no terminal approves it as it approves the rest, and is shown it first, as a scope change
// is (WW-D20).
func TestTheGateHandsThePrompterTheWritableMounts(t *testing.T) {
	src := mountSourceDir(t, "data")
	ws := approvalWorkspace(t)
	cfg := decode(t, `{"mounts": [{"host": "`+src+`", "at": "/ctx/data", "write": true}, "`+src+`:/ctx/ro"]}`)
	p := &reportPrompter{accept: true}
	ok, err := CheckConfigAndScopeChanges(ws, cfg, nil, true, false, p)
	if err != nil || !ok || p.got == nil {
		t.Fatalf("ok=%v err=%v report=%v", ok, err, p.got)
	}
	if len(p.got.WritableMounts) != 1 || !strings.Contains(p.got.WritableMounts[0], src) ||
		!strings.Contains(p.got.WritableMounts[0], "/ctx/data") {
		t.Fatalf("WritableMounts = %q, want the one writable mount, host and jail path", p.got.WritableMounts)
	}

	ws2 := approvalWorkspace(t)
	plain := &reportPrompter{accept: true}
	if ok, err := CheckConfigAndScopeChanges(ws2, decode(t, `{"mounts": ["`+src+`:/ctx/ro"]}`), nil, true, false, plain); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if len(plain.got.WritableMounts) != 0 {
		t.Fatalf("a read-only config reported writable mounts: %q", plain.got.WritableMounts)
	}

	ws3 := approvalWorkspace(t)
	ar := &acceptReporter{}
	ok, err = CheckConfigAndScopeChanges(ws3, cfg, nil, false, true, ar)
	if err != nil || !ok {
		t.Fatalf("--accept-config-changes refused a writable mount: ok=%v err=%v", ok, err)
	}
	if ar.accepted == nil || len(ar.accepted.WritableMounts) != 1 {
		t.Fatalf("--accept-config-changes recorded a writable mount without showing it: %+v", ar.accepted)
	}

	// The refusal with no terminal and no flag carries it too.
	ws4 := approvalWorkspace(t)
	_, err = CheckConfigAndScopeChanges(ws4, cfg, nil, false, false, nil)
	changed, isChanged := err.(*ChangedNonInteractiveError)
	if !isChanged || len(changed.WritableMounts) != 1 || !strings.Contains(changed.Error(), "WRITABLE HOST MOUNT") {
		t.Fatalf("non-interactive refusal = %v, want it to carry and name the writable mount", err)
	}
}

// THE FENCE SEES A TOML PROJECT. The broker mount fence refuses a workspace mount reaching a
// brokered loophole's fenced path and only discloses a user one, so it must find the entry in
// yolo-jail.toml; were its re-read JSONC-only, a TOML project's entry would be judged as the
// user's and pass with a warning. An allowed writable request is fenced like any other entry.
func TestTheMountFenceRefusesATOMLWorkspaceMount(t *testing.T) {
	for _, el := range []string{"'%HOME%/.config/gh:/ctx/gh'", "{ host = '%HOME%/.config/gh', at = '/ctx/gh', write = true }"} {
		h := newMountsHost(t)
		t.Setenv("GH_CONFIG_DIR", "")
		if err := os.MkdirAll(filepath.Join(h.home, ".config", "gh"), 0o755); err != nil {
			t.Fatal(err)
		}
		h.userTOML(t, allowMountsLines)
		h.workspaceFile(t, paths.WorkspaceConfigTOMLName, "mounts = ["+strings.ReplaceAll(el, "%HOME%", h.home)+"]\n")
		cfg, err := LoadConfig(h.ws, true, func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		errs, _ := ValidateConfig(cfg, h.ws, brokeredResolver)
		if !strings.Contains(joined(errs), "A workspace config may not mount it") {
			t.Fatalf("TOML workspace %s reaching the gh config was not fenced: %q", el, errs)
		}
	}
}

// The gate's own read of a TOML project carries the request into the report, so the launch's
// prompt shows it: ReadWorkspaceForGate is what runConfigGate hands ApproveConfigAndScope.
func TestTheGateReadOfATOMLProjectReportsItsWritableMount(t *testing.T) {
	src := mountSourceDir(t, "data")
	h := newMountsHost(t)
	h.userTOML(t, allowMountsLines)
	h.workspaceFile(t, paths.WorkspaceConfigTOMLName, writeRequest(src))
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	p := &reportPrompter{accept: false}
	if ok, err := CheckConfigAndScopeChanges(h.ws, read.Config, nil, true, false, p); err != nil || ok {
		t.Fatalf("ok=%v err=%v; a declined first launch must not proceed", ok, err)
	}
	if p.got == nil || len(p.got.WritableMounts) != 1 || !strings.Contains(p.got.WritableMounts[0], "/ctx/data") {
		t.Fatalf("report = %+v, want the TOML project's writable mount", p.got)
	}
}
