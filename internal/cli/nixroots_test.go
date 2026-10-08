package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
	"github.com/mschulkind-oss/yolo-jail/internal/nixroots/nixrootstest"
)

// nixRootsFixture is a jail whose workspace maps to /host/proj, a fake store with one path
// and a fake daemon.
func nixRootsFixture(t *testing.T) (env nixRootsEnv, ws, storePath string, d *nixrootstest.Daemon) {
	t.Helper()
	ws, _ = filepath.EvalSymlinks(t.TempDir())
	store, _ := filepath.EvalSymlinks(t.TempDir())
	storePath = filepath.Join(store, "0123456789abcdfghijklmnpqrsvwxyz-probe")
	if err := os.WriteFile(storePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	d = nixrootstest.Start(t, nixrootstest.Options{})
	vars := map[string]string{
		nixroots.MapEnv:          nixroots.HostMap{ws: "/host/proj"}.Encode(),
		"NIX_DAEMON_SOCKET_PATH": d.Socket,
		"NIX_STORE_DIR":          store,
	}
	env = nixRootsEnv{inJail: true, workspace: ws, getenv: func(k string) string { return vars[k] },
		now: func() time.Time { return time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC) }}
	return env, ws, storePath, d
}

func runNR(t *testing.T, env nixRootsEnv, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := nixRootsMain(append([]string{"nix-roots"}, args...), env, &out, &errw)
	return rc, out.String(), errw.String()
}

func TestNixRootsKeepListRelease(t *testing.T) {
	env, ws, sp, d := nixRootsFixture(t)
	link := filepath.Join(ws, "result")
	if err := os.Symlink(sp, link); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runNR(t, env, "keep", link)
	if rc != 0 || !strings.Contains(out, "Kept "+link) {
		t.Fatalf("keep rc=%d out=%q err=%q", rc, out, errs)
	}
	id := nixroots.RootID(link)
	if !slices.Contains(d.Roots(), "/host/proj/.yolo/nix-roots/links/"+id) {
		t.Errorf("daemon got %v", d.Roots())
	}
	// keep makes the watcher's handoff: pin, register, fence, on one connection.
	if ops := d.Ops(); len(ops) != 3 || !strings.HasSuffix(ops[0], ":temp:"+sp) ||
		!strings.HasSuffix(ops[2], ":temp:"+sp) || !strings.Contains(ops[1], ":indirect:") {
		t.Errorf("keep's daemon ops = %v", ops)
	}

	rc, out, _ = runNR(t, env, "list", "--format", "json")
	var rows []map[string]any
	if rc != 0 || json.Unmarshal([]byte(out), &rows) != nil || len(rows) != 1 ||
		rows[0]["source_host"] != "/host/proj/result" || rows[0]["expires"] == nil {
		t.Fatalf("list json rc=%d %q", rc, out)
	}
	rc, out, _ = runNR(t, env, "list")
	if rc != 0 || !strings.Contains(out, id) || !strings.Contains(out, "1 of 64") {
		t.Errorf("list text rc=%d %q", rc, out)
	}

	rc, out, _ = runNR(t, env, "release", link)
	if rc != 0 || !strings.Contains(out, "Released "+id) {
		t.Fatalf("release rc=%d %q", rc, out)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("release removed the user's link")
	}
}

func TestNixRootsKeepRefusesWhatCannotBeARoot(t *testing.T) {
	env, ws, sp, _ := nixRootsFixture(t)
	plain := filepath.Join(ws, "plain")
	_ = os.WriteFile(plain, nil, 0o644)
	alias := filepath.Join(ws, "alias")
	_ = os.Symlink("profile-1-link", alias)
	etc := filepath.Join(ws, "etc")
	_ = os.Symlink("/etc/hosts", etc)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	scratch := filepath.Join(outside, "result")
	_ = os.Symlink(sp, scratch)
	for _, c := range []struct{ link, want string }{
		{plain, "not a symlink"},
		{alias, "not straight into the store"},
		{etc, "not straight into the store"},
		{scratch, "under no directory the host can see"},
	} {
		rc, _, errs := runNR(t, env, "keep", c.link)
		if rc == 0 || !strings.Contains(errs, c.want) {
			t.Errorf("keep %s: rc=%d err=%q, want %q", c.link, rc, errs, c.want)
		}
	}
}

func TestNixRootsKeepWithoutAMapNamesTheNextStep(t *testing.T) {
	env, ws, sp, _ := nixRootsFixture(t)
	env.getenv = func(string) string { return "" }
	link := filepath.Join(ws, "result")
	_ = os.Symlink(sp, link)
	rc, _, errs := runNR(t, env, "keep", link)
	if rc == 0 || !strings.Contains(errs, "restart the jail") {
		t.Errorf("rc=%d err=%q", rc, errs)
	}
}

func TestNixRootsKeepOnTheHostPointsAtNixStoreAddRoot(t *testing.T) {
	env, ws, _, _ := nixRootsFixture(t)
	env.inJail = false
	rc, _, errs := runNR(t, env, "keep", filepath.Join(ws, "result"))
	if rc == 0 || !strings.Contains(errs, "nix-store --add-root") {
		t.Errorf("rc=%d err=%q", rc, errs)
	}
}

// The host can list and release what a jail kept: same directory, no daemon needed.
func TestNixRootsHostListsAndReleasesAJailsRoots(t *testing.T) {
	env, ws, sp, _ := nixRootsFixture(t)
	link := filepath.Join(ws, "result")
	_ = os.Symlink(sp, link)
	if rc, _, errs := runNR(t, env, "keep", link); rc != 0 {
		t.Fatal(errs)
	}
	host := env
	host.inJail = false
	rc, out, _ := runNR(t, host, "list")
	if rc != 0 || !strings.Contains(out, link) {
		t.Fatalf("host list rc=%d %q", rc, out)
	}
	rc, out, _ = runNR(t, host, "release", "--all")
	if rc != 0 || !strings.Contains(out, "Released") {
		t.Fatalf("host release rc=%d %q", rc, out)
	}
}

func TestNixRootsWithoutAWorkspaceRefuses(t *testing.T) {
	env, _, _, _ := nixRootsFixture(t)
	env.workspace = ""
	if rc, _, errs := runNR(t, env, "list"); rc == 0 || !strings.Contains(errs, "Run it from inside a workspace") {
		t.Errorf("rc=%d err=%q", rc, errs)
	}
}

// The front door's wiring: the registry entry is what makes the command exist.
func TestNixRootsIsRegistered(t *testing.T) {
	if _, ok := registry["nix-roots"]; !ok {
		t.Fatal("nix-roots is not in the dispatch registry")
	}
}

// roots.json is jail-written; the host prints none of its control characters.
func TestNixRootsListEscapesWhatTheJailWrote(t *testing.T) {
	env, ws, sp, _ := nixRootsFixture(t)
	link := filepath.Join(ws, "result")
	_ = os.Symlink(sp, link)
	if rc, _, errs := runNR(t, env, "keep", link); rc != 0 {
		t.Fatal(errs)
	}
	file := filepath.Join(ws, ".yolo", "nix-roots", "roots.json")
	b, _ := os.ReadFile(file)
	b = []byte(strings.Replace(string(b), `"by": "keep"`, `"by": "\u001b]52;c;cGF5bG9hZA==\u0007"`, 1))
	if err := os.WriteFile(file, b, 0o644); err != nil {
		t.Fatal(err)
	}
	host := env
	host.inJail = false
	_, out, _ := runNR(t, host, "list")
	if strings.ContainsRune(out, 0x1b) || strings.ContainsRune(out, 0x07) {
		t.Errorf("list printed a raw control character: %q", out)
	}
}
