package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// layerFixture is a --user-layer file in the old format, with comments, a path pack whose
// readable handle renames it and a package, beside a user config in the format named ("toml",
// "jsonc" or "none") and a workspace with an old project file, which a layer migration must leave
// alone. The pack's lock entry is recorded under its old name, as a launch passing the layer
// leaves it.
func layerFixture(t *testing.T, user string) (home, dir, ws, layer string) {
	t.Helper()
	home = env(t)
	dir = filepath.Join(home, ".config", "yolo-jail")
	packDir := filepath.Join(home, "dots", "yolo-packs", "pi-fork")
	write(t, filepath.Join(packDir, "pack.toml"), "name = \"pi-fork\"\n")
	switch user {
	case "toml":
		write(t, filepath.Join(dir, paths.UserConfigTOMLName), "[install]\nnix = [\"user\"]\n")
	case "jsonc":
		write(t, filepath.Join(dir, paths.UserConfigLegacyJSONCName), `{"packages": ["user"]}`+"\n")
	default:
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ws = realDir(t)
	write(t, filepath.Join(ws, paths.WorkspaceConfigLegacyJSONCName), `{"packages": ["project"]}`+"\n")
	layer = filepath.Join(home, "layers", "work.jsonc")
	write(t, layer, "{\n  // the forked agent, for work\n  \"packs\": [\"~/dots/yolo-packs/pi-fork\"],\n"+
		"  // work tools\n  \"packages\": [\"layer\"]\n}\n")
	userConfig, _ := config.UserConfigFile()
	if err := packsrc.WithLock(paths.PacksDir(), packsrc.LockPath(userConfig), nil, func(l *packsrc.Lock) (bool, error) {
		l.Set(packsrc.LockEntry{Name: "pi-fork", Source: "file://" + packDir})
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	return home, dir, ws, layer
}

// layerPacks is the pack selection a launch passing `--user-layer layer` reads.
func layerPacks(t *testing.T, layer string) []config.PackEntry {
	t.Helper()
	var entries []config.PackEntry
	var err error
	withUserLayer(layer, func() { entries, err = config.LoadPacks(func(string) {}) })
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// The owner's ruling of 2026-11-01 (RC-D46, amended): `yolo config migrate FILE` converts exactly
// the named --user-layer file. The TOML is written beside it, the original is kept byte for byte
// as <name>.pre-migrate, the path pack becomes its readable handle with the source the layer
// itself carries, its records follow the new name, and the user config and the workspace's files
// are not touched. A launch passing the new layer selects the same pack directory and the same
// packages over every kind of user config.
func TestAUserLayerIsConvertedOnItsOwn(t *testing.T) {
	for _, user := range []string{"toml", "jsonc", "none"} {
		t.Run(user, func(t *testing.T) {
			home, dir, ws, layer := layerFixture(t, user)
			original, _ := os.ReadFile(layer)
			userBefore, wsBefore := treeState(t, dir), treeState(t, ws)
			packsBefore := layerPacks(t, layer)

			p := plan(t, Request{UserConfigDir: dir, Workspace: ws, Layer: layer})
			if len(p.Targets) != 1 {
				t.Fatalf("targets = %+v, want only the layer", p.Targets)
			}
			tg := target(t, p, layer)
			text := string(tg.Bytes)
			for _, want := range []string{"# the forked agent, for work", "yolo-packs/pi-fork", "[pack_sources.yolo-packs]", `path = "~/dots/yolo-packs"`, `nix = ["layer"]`} {
				if !strings.Contains(text, want) {
					t.Errorf("the converted layer does not carry %q:\n%s", want, text)
				}
			}
			if len(p.Renames) != 1 || p.Renames[0].Old != "pi-fork" || p.Renames[0].New != "yolo-packs/pi-fork" {
				t.Errorf("renames = %+v, want pi-fork → yolo-packs/pi-fork", p.Renames)
			}
			res, err := Apply(p, ApplyOptions{})
			if err != nil {
				t.Fatal(err)
			}
			newLayer := filepath.Join(home, "layers", "work.toml")
			if !reflect.DeepEqual(res.Written, []string{newLayer}) {
				t.Errorf("written = %v, want %s", res.Written, newLayer)
			}
			if kept, _ := os.ReadFile(layer + ".pre-migrate"); string(kept) != string(original) {
				t.Errorf("the original is not kept byte for byte:\n%s", kept)
			}
			if exists(layer) {
				t.Error("the old layer name is still there")
			}
			if got := treeState(t, ws); !reflect.DeepEqual(got, wsBefore) {
				t.Errorf("the workspace changed:\ngot  %v\nwant %v", got, wsBefore)
			}
			for name, state := range userBefore {
				if name == "packs.lock.json" || strings.HasPrefix(name, ".") {
					continue
				}
				if treeState(t, dir)[name] != state {
					t.Errorf("the user config directory's %s changed", name)
				}
			}
			after := layerPacks(t, newLayer)
			if len(after) != len(packsBefore) {
				t.Fatalf("selection before %+v, after %+v", packsBefore, after)
			}
			var found bool
			for _, e := range after {
				if e.Name == "yolo-packs/pi-fork" {
					found = true
					if mustEval(t, e.LocalPath) != mustEval(t, filepath.Join(home, "dots", "yolo-packs", "pi-fork")) {
						t.Errorf("the converted pack resolves to %s", e.LocalPath)
					}
				}
			}
			if !found {
				t.Errorf("the converted layer does not select yolo-packs/pi-fork: %+v", after)
			}
			userConfig, _ := config.UserConfigFile()
			lock, err := packsrc.LoadLock(packsrc.LockPath(userConfig))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := lock.Get("yolo-packs/pi-fork"); !ok {
				t.Errorf("the lock entry did not follow the rename: %+v", lock.Packs)
			}
			var read *config.MigrationGraphRead
			withUserLayer(newLayer, func() {
				cfg, err := config.LoadConfig(ws, true, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				read = &config.MigrationGraphRead{Composed: cfg}
			})
			install, _ := read.Composed.Get("install")
			if got := describe(install); !strings.Contains(got, `"layer"`) || !strings.Contains(got, `"project"`) {
				t.Errorf("a launch with the converted layer reads install = %s", got)
			}
		})
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	got, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A layer the user keeps in a dotfiles tree and links to is converted as RC-D41 converts a linked
// user config: the link itself moves to <name>.pre-migrate, the TOML is a regular file beside it,
// and the dotfiles tree stays byte-identical and clean.
func TestALinkedUserLayerIsMovedAndItsTargetIsNeverTouched(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, paths.UserConfigTOMLName), "[install]\nnix = [\"user\"]\n")
	repo := filepath.Join(home, "dotfiles")
	write(t, filepath.Join(repo, "yolo", "work.jsonc"), `{"packages": ["layer"]} // work`+"\n")
	dotfilesRepo(t, repo)
	dotsBefore := treeState(t, repo)
	layer := filepath.Join(home, "work.jsonc")
	if err := os.Symlink("dotfiles/yolo/work.jsonc", layer); err != nil {
		t.Fatal(err)
	}
	p := plan(t, Request{UserConfigDir: dir, Layer: layer})
	res, err := Apply(p, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := treeState(t, repo); !reflect.DeepEqual(got, dotsBefore) {
		t.Fatalf("the dotfiles tree changed:\ngot  %v\nwant %v", got, dotsBefore)
	}
	if s := gitIn(t, repo, "status", "--porcelain", "--ignored"); s != "" {
		t.Fatalf("git status in the dotfiles repository is not clean:\n%s", s)
	}
	if text, err := os.Readlink(layer + ".pre-migrate"); err != nil || text != "dotfiles/yolo/work.jsonc" {
		t.Fatalf("work.jsonc.pre-migrate = %q (%v), want the original link", text, err)
	}
	if info, err := os.Lstat(filepath.Join(home, "work.toml")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("work.toml is not a regular file: %v", err)
	}
	if len(res.Links) != 1 || res.Links[0].Target != filepath.Join(repo, "yolo", "work.jsonc") {
		t.Fatalf("Links = %+v", res.Links)
	}
}

// A layer that would not convert to what it says is refused, nothing changes, and the refusal
// names the next step; so is a TOML file, a file the user config also reads, and a converted one.
func TestALayerThatCannotConvertChangesNothing(t *testing.T) {
	home, dir, ws, _ := layerFixture(t, "toml")
	bad := filepath.Join(home, "layers", "solo.jsonc")
	write(t, bad, `{"packs": ["~/solo"]}`+"\n")
	before := treeState(t, home)
	g, err := Discover(Request{UserConfigDir: dir, Workspace: ws, Layer: bad})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var unequal *InequalityError
	if !errors.As(err, &unequal) {
		t.Fatalf("Plan = %v, want the inequality refusal", err)
	}
	if !strings.Contains(err.Error(), "run `yolo config migrate` again") {
		t.Errorf("the refusal names no next step: %v", err)
	}
	if _, err := Apply(p, ApplyOptions{}); err == nil {
		t.Fatal("Apply wrote a plan that did not prove equal")
	}
	if got := treeState(t, home); !reflect.DeepEqual(got, before) {
		t.Fatalf("a refused layer changed files:\ngot  %v\nwant %v", got, before)
	}

	toml := filepath.Join(home, "layers", "done.toml")
	write(t, toml, "[install]\nnix = [\"x\"]\n")
	if _, err := Discover(Request{UserConfigDir: dir, Layer: toml}); err == nil || !strings.Contains(err.Error(), "--user-layer "+toml) {
		t.Errorf("a TOML layer: %v, want a refusal naming the launch", err)
	}

	shared := filepath.Join(dir, "shared.jsonc")
	write(t, shared, `{"packages": ["shared"]}`+"\n")
	write(t, filepath.Join(dir, paths.UserConfigTOMLName), "include_if_found = [\"shared.jsonc\"]\n")
	if _, err := Discover(Request{UserConfigDir: dir, Layer: shared}); err == nil || !strings.Contains(err.Error(), "with no file") {
		t.Errorf("a layer the user config includes: %v, want a refusal naming `yolo config migrate` with no file", err)
	}

	gone := filepath.Join(home, "layers", "gone.jsonc")
	write(t, filepath.Join(home, "layers", "gone.toml"), "[install]\nnix = [\"x\"]\n")
	if _, err := Discover(Request{UserConfigDir: dir, Layer: gone}); err == nil || !strings.Contains(err.Error(), "converted already") {
		t.Errorf("a converted layer named again: %v, want one naming its twin", err)
	}
}

// The converted layer carries every source its packs need, so it reads the same over any user
// config. A source the user config already writes with the same table keeps the user config's
// name; a name the user config gives another directory is never taken, since the layer's table
// would replace it.
func TestALayersSourcesAgreeWithTheUserConfigs(t *testing.T) {
	for _, c := range []struct {
		name, userSources, want string
	}{
		{"same table", "[pack_sources.mine]\npath = \"~/dots/yolo-packs\"\nbare = true\n", "mine/pi-fork"},
		{"name taken", "[pack_sources.yolo-packs]\npath = \"~/elsewhere\"\nbare = true\n", "yolo-packs-2/pi-fork"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, dir, _, layer := layerFixture(t, "toml")
			write(t, filepath.Join(dir, paths.UserConfigTOMLName), "[install]\nnix = [\"user\"]\n\n"+c.userSources)
			p := plan(t, Request{UserConfigDir: dir, Layer: layer})
			if text := string(target(t, p, layer).Bytes); !strings.Contains(text, c.want) {
				t.Fatalf("the converted layer does not name %s:\n%s", c.want, text)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range layerPacks(t, strings.TrimSuffix(layer, ".jsonc")+".toml") {
				names = append(names, e.Name)
			}
			if !strings.Contains(strings.Join(names, " "), c.want) {
				t.Errorf("a launch with the converted layer selects %v, want %s", names, c.want)
			}
		})
	}
}

// A layer migration killed part way is finished by the next `yolo config migrate`, the read-back
// included: the journal carries the layer, so the converted file is read back over the user
// config, not as one.
func TestAKilledLayerMigrationRollsForward(t *testing.T) {
	_, dir, ws, layer := layerFixture(t, "jsonc")
	req := Request{UserConfigDir: dir, Workspace: ws, Layer: layer}
	p := plan(t, req)
	if _, err := Apply(p, ApplyOptions{StopAfter: func(int, string) bool { return true }}); err == nil {
		t.Fatal("the seam did not stop the migration")
	}
	journal := Interrupted(req)
	if journal != filepath.Join(dir, config.MigrationJournalName) {
		t.Fatalf("journal = %q, want it in the user config directory, where the launch looks", journal)
	}
	if config.InterruptedMigration(ws) == "" {
		t.Error("a launch would not see the part-way layer migration")
	}
	res, err := Resume(journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PackRecords) == 0 {
		t.Error("Resume did not move the renamed pack's records")
	}
	if exists(journal) || exists(layer) || !exists(strings.TrimSuffix(layer, ".jsonc")+".toml") {
		t.Fatal("the resumed layer migration did not finish")
	}
}

// RC-D51: `yolo config migrate FILE` converts FILE and the old files it includes, and the
// converted FILE names the converted include; `--only FILE` (Request.Only) converts FILE alone,
// leaving its include an old file the converted FILE still names. Either way a launch passing the
// converted layer reads what it read before. Fails if Only stops skipping the include walk (the
// include is then converted too), or if the default stops converting it.
func TestAFileIsConvertedWithItsIncludesUnlessOnly(t *testing.T) {
	for _, only := range []bool{false, true} {
		name := "with its includes"
		if only {
			name = "--only"
		}
		t.Run(name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			write(t, filepath.Join(dir, paths.UserConfigTOMLName), "[install]\nnix = ['user']\n")
			ws := realDir(t)
			layer := filepath.Join(home, "layers", "work.jsonc")
			include := filepath.Join(home, "layers", "more.jsonc")
			write(t, layer, `{"packages": ["layer"], "include_if_found": ["more.jsonc"]}`+"\n")
			write(t, include, `{"packages": ["more"], "env_sources": [{"A": "more"}]}`+"\n")
			includeBytes, _ := os.ReadFile(include)
			read := func(layer string) string {
				t.Helper()
				var text string
				withUserLayer(layer, func() {
					merged, err := config.LoadConfig(ws, true, func(string) {})
					if err != nil {
						t.Fatal(err)
					}
					text, err = config.SnapshotJSON(merged)
					if err != nil {
						t.Fatal(err)
					}
				})
				return text
			}
			before := read(layer)
			if !strings.Contains(before, `"more"`) || !strings.Contains(before, `"layer"`) {
				t.Fatalf("the fixture reads %s", before)
			}
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws, Layer: layer, Only: only})
			if len(p.NotConverted) > 0 {
				t.Fatalf("not converted: %+v", p.NotConverted)
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			converted := filepath.Join(home, "layers", "work.toml")
			data, err := os.ReadFile(converted)
			if err != nil {
				t.Fatal(err)
			}
			if after := read(converted); after != before {
				t.Errorf("the converted layer reads differently:\n  before %s\n  after  %s", before, after)
			}
			_, includeErr := os.Stat(filepath.Join(home, "layers", "more.toml"))
			if only {
				if now, _ := os.ReadFile(include); !reflect.DeepEqual(now, includeBytes) || includeErr == nil {
					t.Errorf("--only touched the include: more.jsonc now %q, more.toml exists %v", now, includeErr == nil)
				}
				if !strings.Contains(string(data), "more.jsonc") {
					t.Errorf("--only's converted layer does not name the old include:\n%s", data)
				}
				return
			}
			if includeErr != nil {
				t.Errorf("the include was not converted with the file: %v", includeErr)
			}
			if !strings.Contains(string(data), "more.toml") || strings.Contains(string(data), "more.jsonc") {
				t.Errorf("the converted layer does not name the converted include:\n%s", data)
			}
		})
	}
}
