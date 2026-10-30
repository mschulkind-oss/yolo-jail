package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

// env isolates a test from the real home and from this jail's own launch: the approval records
// live under HOME, and YOLO_VERSION makes config read as an in-jail process.
func env(t *testing.T) (home string) {
	t.Helper()
	home = realDir(t)
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	return home
}

func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRemotes(t *testing.T, ws string, urls ...string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("[core]\n\tbare = false\n")
	for i, u := range urls {
		b.WriteString("[remote \"r" + string(rune('a'+i)) + "\"]\n\turl = " + u + "\n")
	}
	write(t, filepath.Join(ws, ".git", "config"), b.String())
	write(t, filepath.Join(ws, ".git", "HEAD"), "ref: refs/heads/main\n")
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func plan(t *testing.T, req Request) *MigrationPlan {
	t.Helper()
	g, err := Discover(req)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	p, err := Plan(g)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return p
}

func target(t *testing.T, p *MigrationPlan, old string) Target {
	t.Helper()
	for _, tg := range p.Targets {
		if tg.Old == old {
			return tg
		}
	}
	t.Fatalf("no target for %s in %+v", old, p.Targets)
	return Target{}
}

// A legacy branch grant scoped to one of two remotes has no request spelling: a request names
// refs on every remote. The plan shows the exact difference and nothing is written.
func TestPlanRefusesOnInequality(t *testing.T) {
	env(t)
	ws := realDir(t)
	gitRemotes(t, ws, "https://github.com/acme/one.git", "git@github.com:acme/two.git")
	old := `{
  "packages": ["just"],
  "brokered": {"git": {"grants": [
    {"kind": "branch", "repo": "github.com/acme/one", "branch": "main"}
  ]}}
}
`
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), old)
	g, err := Discover(Request{Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("Plan error = %v, want an InequalityError", err)
	}
	if p == nil || p.Equal {
		t.Fatalf("plan must be unequal: %+v", p)
	}
	found := false
	for _, d := range p.Differences {
		if d.Layer == "project config" && d.Path == "brokered" && d.After == "(absent)" &&
			strings.Contains(d.Before, `"repo": "github.com/acme/one"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("differences do not name the scoped grant exactly: %+v", p.Differences)
	}
	msg := err.Error()
	for _, want := range []string{"github.com/acme/one", "every remote of this workspace", "nothing was written", "yolo config migrate"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q:\n%s", want, msg)
		}
	}
	if _, err := Apply(p, ApplyOptions{}); err == nil {
		t.Fatal("Apply must refuse an unequal plan")
	}
	if exists(filepath.Join(ws, "yolo-jail.toml")) || exists(filepath.Join(ws, "yolo-jail.jsonc.pre-migrate")) {
		t.Fatal("an unequal plan wrote a file")
	}
	if data, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.jsonc")); string(data) != old {
		t.Fatal("the old file changed")
	}

	// Control: with the workspace's one remote, the same grant is the request and the plan is equal.
	ws2 := realDir(t)
	gitRemotes(t, ws2, "https://github.com/acme/one.git")
	write(t, filepath.Join(ws2, "yolo-jail.jsonc"), old)
	p2 := plan(t, Request{Workspace: ws2})
	if !p2.Equal {
		t.Fatalf("one-remote plan unequal: %+v", p2.Differences)
	}
	got := string(target(t, p2, filepath.Join(ws2, "yolo-jail.jsonc")).Bytes)
	if !strings.Contains(got, "[requests.git]\npush = [\"main\"]") || !strings.Contains(got, "[install]\nnix = [\"just\"]") {
		t.Fatalf("converted file:\n%s", got)
	}
}

// Every comment survives, as # lines, in document order: a template block, standalone and inline
// comments, and one after a key the conversion drops.
func TestPlanKeepsEveryCommentInOrder(t *testing.T) {
	env(t)
	dir := filepath.Join(realDir(t), ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `/*
 * Template: every key is optional.
 *
 *   packs: what to install
 */
{
  // standalone before packs
  "packs": ["claude"], // inline after packs
  /* block before resources */
  "resources": {"memory": "8g"},
  // before a dropped key
  "convert_later": true,
  // after the dropped key, before network
  "network": {"mode": "bridge"}
  // trailing comment
}
`)
	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("unequal: %+v %+v", p.Differences, p.NotConverted)
	}
	tg := target(t, p, filepath.Join(dir, "config.jsonc"))
	got := string(tg.Bytes)
	want := []string{
		"# Template: every key is optional.",
		"#   packs: what to install",
		"# standalone before packs",
		"# inline after packs",
		"# block before resources",
		"# before a dropped key",
		"# after the dropped key, before network",
		"# trailing comment",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("comment %q missing or out of order in:\n%s", w, got)
		}
		at += i + len(w)
	}
	if tg.Comments != 7 {
		t.Errorf("Comments = %d, want 7", tg.Comments)
	}
	if strings.Contains(got, "convert_later") {
		t.Errorf("convert_later written to TOML:\n%s", got)
	}
	if _, err := configdoc.Parse(tg.Bytes, configdoc.TOML); err != nil {
		t.Fatalf("output is not TOML: %v\n%s", err, got)
	}
	if !strings.Contains(got, "[machine.resources]\nmemory = \"8g\"") {
		t.Errorf("resources not moved:\n%s", got)
	}
}

func TestApplyWritesPreMigrateAndRestoresOnFailure(t *testing.T) {
	env(t)
	ws := realDir(t)
	top := "{\n  // top\n  \"packages\": [\"just\"],\n  \"include_if_found\": [\"extra.jsonc\"]\n}\n"
	extra := "{\"mise_tools\": {\"lua\": \"5.4.9\"}}\n"
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), top)
	write(t, filepath.Join(ws, "extra.jsonc"), extra)

	p := plan(t, Request{Workspace: ws})
	steps := 0
	_, err := Apply(p, ApplyOptions{failAfter: func(step string) error {
		steps++
		if steps == 3 {
			return errors.New("injected failure at " + step)
		}
		return nil
	}})
	if err == nil || !strings.Contains(err.Error(), "injected failure") || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("Apply error = %v", err)
	}
	for path, want := range map[string]string{"yolo-jail.jsonc": top, "extra.jsonc": extra} {
		if data, err := os.ReadFile(filepath.Join(ws, path)); err != nil || string(data) != want {
			t.Fatalf("%s not restored: %q %v", path, data, err)
		}
	}
	for _, path := range []string{"yolo-jail.toml", "extra.toml", "yolo-jail.jsonc.pre-migrate", "extra.jsonc.pre-migrate"} {
		if exists(filepath.Join(ws, path)) {
			t.Fatalf("%s left behind after a failed migration", path)
		}
	}

	p = plan(t, Request{Workspace: ws})
	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Written) != 2 || len(res.Backups) != 2 {
		t.Fatalf("result = %+v", res)
	}
	for path, want := range map[string]string{"yolo-jail.jsonc.pre-migrate": top, "extra.jsonc.pre-migrate": extra} {
		if data, err := os.ReadFile(filepath.Join(ws, path)); err != nil || string(data) != want {
			t.Fatalf("%s does not hold the original: %q %v", path, data, err)
		}
	}
	if exists(filepath.Join(ws, "yolo-jail.jsonc")) || exists(filepath.Join(ws, "extra.jsonc")) {
		t.Fatal("an original stayed under its active name")
	}
	data, _ := os.ReadFile(filepath.Join(ws, "yolo-jail.toml"))
	if !strings.Contains(string(data), `include_if_found = ["extra.toml"]`) || !strings.Contains(string(data), "# top") {
		t.Fatalf("yolo-jail.toml:\n%s", data)
	}
	cfg, err := config.LoadWorkspaceConfig(ws, true, func(m string) { t.Errorf("warn: %s", m) })
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := cfg.Get("install"); describe(v) != `{"nix": ["just"], "mise": {"lua": "5.4.9"}}` {
		t.Fatalf("migrated workspace reads install = %s", describe(v))
	}
}

// The approval record and the boot baseline are respelled to what the launch computes from the new
// files, so the gate sees no change; a record that already differed is left alone.
func TestApplyRespellsApprovalAndBootRecords(t *testing.T) {
	env(t)
	ws := realDir(t)
	write(t, filepath.Join(ws, "dev.env"), "A=1\n")
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"packages": ["just"], "env_sources": ["dev.env"]}`+"\n")
	approved, err := config.ApprovalRecordPart(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteApprovalRecord(ws, approved); err != nil {
		t.Fatal(err)
	}
	boot, err := config.BootBaselinePart(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteBootBaseline(ws, boot); err != nil {
		t.Fatal(err)
	}
	gateSeesNoChange := func() error {
		read, err := config.ReadWorkspaceForGate(ws)
		if err != nil {
			return err
		}
		ok, err := config.CheckConfigChanges(ws, read.Config, false, false, nil)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("gate did not pass")
		}
		return nil
	}
	if err := gateSeesNoChange(); err != nil {
		t.Fatalf("gate before migration: %v", err)
	}

	p := plan(t, Request{Workspace: ws})
	for _, r := range p.Records {
		if r.State != RecordRespell {
			t.Fatalf("record %s planned %v (%s), want respell", r.Kind, r.State, r.Why)
		}
	}
	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := gateSeesNoChange(); err != nil {
		t.Fatalf("gate after migration: %v", err)
	}
	record, _, _ := config.ReadApprovalRecord(ws)
	if record == approved || !strings.Contains(record, `"files"`) {
		t.Fatalf("approval record not respelled:\n%s", record)
	}
	baseline, _, _ := config.ReadBootBaseline(ws)
	if now, _ := config.BootBaselinePart(ws); baseline != now || baseline == boot {
		t.Fatalf("boot baseline not respelled:\n%s", baseline)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %+v", res.Records)
	}

	// A record that already differs from the old config is not blessed by the respelling.
	ws2 := realDir(t)
	write(t, filepath.Join(ws2, "yolo-jail.jsonc"), `{"packages": ["just"]}`+"\n")
	if err := config.WriteApprovalRecord(ws2, `{"packages": ["other"]}`); err != nil {
		t.Fatal(err)
	}
	p2 := plan(t, Request{Workspace: ws2})
	if p2.Records[0].State != RecordLeft {
		t.Fatalf("drifted record planned %v", p2.Records[0].State)
	}
	if _, err := Apply(p2, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if record, _, _ := config.ReadApprovalRecord(ws2); record != `{"packages": ["other"]}` {
		t.Fatalf("drifted record rewritten: %s", record)
	}
}

// A symlinked top-level file and a symlinked include are converted where they physically live
// (the dotfiles copy), the include entry is respelled, and each of the user's links is renamed to
// the new name, reaching the new file (RC-D41).
func TestPlanRespellsIncludesThroughSymlink(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles", "yolo")
	write(t, filepath.Join(dots, "config.jsonc"), `{"packs": ["claude"], "include_if_found": ["work.jsonc"]}`+"\n")
	write(t, filepath.Join(dots, "work.jsonc"), `{"packages": ["ripgrep"]}`+"\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../dotfiles/yolo/config.jsonc", filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../dotfiles/yolo/work.jsonc", filepath.Join(dir, "work.jsonc")); err != nil {
		t.Fatal(err)
	}

	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("unequal: %+v %+v", p.Differences, p.NotConverted)
	}
	top := target(t, p, filepath.Join(dots, "config.jsonc"))
	if top.New != filepath.Join(dots, "config.toml") || top.Link == nil ||
		top.Link.Path != filepath.Join(dir, "config.toml") || top.Link.Target != "../../dotfiles/yolo/config.toml" {
		t.Fatalf("top target = %+v link %+v", top, top.Link)
	}
	if !strings.Contains(string(top.Bytes), `include_if_found = ["work.toml"]`) {
		t.Fatalf("include not respelled:\n%s", top.Bytes)
	}
	work := target(t, p, filepath.Join(dots, "work.jsonc"))
	if work.New != filepath.Join(dots, "work.toml") || work.Link == nil || work.Link.Path != filepath.Join(dir, "work.toml") {
		t.Fatalf("work target = %+v", work)
	}

	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"config.jsonc", "work.jsonc"} {
		if _, err := os.Lstat(filepath.Join(dir, link)); !os.IsNotExist(err) {
			t.Fatalf("old link %s was not renamed: %v", link, err)
		}
	}
	if len(res.Links) != 2 || res.Links[0].From != filepath.Join(dir, "config.jsonc") || res.Links[0].To != filepath.Join(dir, "config.toml") {
		t.Fatalf("Links = %+v, want each old link with its new name", res.Links)
	}
	for _, name := range []string{"config.toml", "work.toml"} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s is not a link: %v", name, err)
		}
	}
	for _, name := range []string{"config.toml", "work.toml"} {
		resolved, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err != nil || resolved != filepath.Join(dots, name) {
			t.Fatalf("%s resolves to %q (%v)", name, resolved, err)
		}
	}
	read, err := config.ReadMigrationGraph(filepath.Join(dir, "config.toml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := read.User.Get("install"); describe(v) != `{"nix": ["ripgrep"]}` {
		t.Fatalf("migrated user graph install = %s", describe(v))
	}
}

// An include this machine does not have is listed with the command to run where it is, and its
// entry keeps its spelling so that machine's own migration finds it.
func TestPlanListsUnreachableInclude(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, "config.jsonc"), `{"include_if_found": ["personal.jsonc", "here.jsonc"], "packs": ["pi"]}`+"\n")
	write(t, filepath.Join(dir, "here.jsonc"), `{"packs": ["claude"]}`+"\n")
	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("unequal: %+v", p.Differences)
	}
	if len(p.Unreachable) != 1 || p.Unreachable[0].Path != filepath.Join(dir, "personal.jsonc") ||
		p.Unreachable[0].ListedIn != filepath.Join(dir, "config.jsonc") {
		t.Fatalf("unreachable = %+v", p.Unreachable)
	}
	text := p.Transcript()
	if !strings.Contains(text, "Not found here (listed in "+filepath.Join(dir, "config.jsonc")+"): "+filepath.Join(dir, "personal.jsonc")) ||
		!strings.Contains(text, "Run `yolo config migrate` on a machine that has it.") {
		t.Fatalf("transcript:\n%s", text)
	}
	got := string(target(t, p, filepath.Join(dir, "config.jsonc")).Bytes)
	if !strings.Contains(got, `include_if_found = ["personal.jsonc", "here.toml"]`) {
		t.Fatalf("converted:\n%s", got)
	}
}

// A TOML file that includes an old file has only that entry respelled.
func TestPlanRespellsTOMLParentByteForByte(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	parent := "# mine\npacks = [\"pi\"]\ninclude_if_found = [ \"old.jsonc\" ]  # keep\n"
	write(t, filepath.Join(dir, "config.toml"), parent)
	write(t, filepath.Join(dir, "old.jsonc"), `{"packages": ["jq"]}`+"\n")
	p := plan(t, Request{UserConfigDir: dir})
	if !p.Equal {
		t.Fatalf("unequal: %+v", p.Differences)
	}
	got := string(target(t, p, filepath.Join(dir, "config.toml")).Bytes)
	if got != strings.Replace(parent, `"old.jsonc"`, `"old.toml"`, 1) {
		t.Fatalf("respelled parent:\n%s", got)
	}
}

// A failure after the old link is removed puts it back, with its own text, and removes its twin.
func TestApplyRestoresARenamedLinkOnFailure(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	dots := filepath.Join(home, "dotfiles", "yolo")
	write(t, filepath.Join(dots, "config.jsonc"), `{"packs": ["claude"]}`+"\n")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const text = "../../dotfiles/yolo/config.jsonc"
	if err := os.Symlink(text, filepath.Join(dir, "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	p := plan(t, Request{UserConfigDir: dir})
	var failedAt string
	_, err := Apply(p, ApplyOptions{failAfter: func(step string) error {
		if strings.HasPrefix(step, "unlink ") {
			failedAt = step
			return errors.New("injected failure at " + step)
		}
		return nil
	}})
	if failedAt == "" || err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("Apply error = %v (failed at %q)", err, failedAt)
	}
	if got, err := os.Readlink(filepath.Join(dir, "config.jsonc")); err != nil || got != text {
		t.Fatalf("old link = %q (%v), want %q back", got, err, text)
	}
	for _, path := range []string{filepath.Join(dir, "config.toml"), filepath.Join(dots, "config.toml"), filepath.Join(dots, "config.jsonc.pre-migrate")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s left behind after a failed migration", path)
		}
	}
}
