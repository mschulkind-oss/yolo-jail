package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// provisioners_test.go covers the `provisioners` key (docs/design/provisioner-sets.md PS-D11):
// read from user scope only, a per-package order replacing the environment's list for its binary,
// every refused name left out by the reader, and validation through ValidateConfig, the call site
// `yolo check` and a launch reach — PS-D3's refusal of a recipe no selected pack ships included.

// TestProvisionerOrderIsTheUserConfigsOrderAndNothingElse: absent and unparseable read as no order;
// the environment's list keeps its written order with refused names left out; a per-package order
// replaces it for its binary alone; and a workspace value is never read — a cloned repository must
// not choose the command yolo installs a program with.
func TestProvisionerOrderIsTheUserConfigsOrderAndNothingElse(t *testing.T) {
	userCfg := hostFloorHome(t)
	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"provisioners": {"host": ["nix"]}}`)
	if got := ProvisionerOrder(ProvisionerEnvHost, "claude"); got != nil {
		t.Errorf("with no user config = %q, want none; a workspace value must never be read", got)
	}
	write(t, userCfg, `{"provisioners": {"host": ["brew"`)
	if got := ProvisionerOrder(ProvisionerEnvHost, "claude"); got != nil {
		t.Errorf("an unparseable user config = %q, want none", got)
	}
	write(t, userCfg, `{"provisioners": {"host": ["nix", "npm", "brew", "nix", 7, "pack"],
	  "per_package": {"claude": {"host": ["brew"]}}}}`)
	if got := strings.Join(ProvisionerOrder(ProvisionerEnvHost, "rg"), " "); got != "nix brew pack" {
		t.Errorf("rg's order = %q, want the host list in written order with refused names left out", got)
	}
	if got := strings.Join(ProvisionerOrder(ProvisionerEnvHost, "claude"), " "); got != "brew" {
		t.Errorf("claude's order = %q, want its per-package order", got)
	}
	if got := ProvisionerOrder("jail", "rg"); got != nil {
		t.Errorf("an environment the config does not write = %q, want none", got)
	}
}

// TestValidateProvisioners: the shapes and names accepted, each refusal naming where it is, the
// workspace spelling refused, and a per-package order naming a provisioner no selected pack ships
// a recipe for refused (PS-D3), naming the recipes that exist.
func TestValidateProvisioners(t *testing.T) {
	userCfg := hostFloorHome(t)
	write(t, userCfg, `{"packs": ["claude", "guardrails"]}`)
	clean := t.TempDir()
	provErrs := func(cfg string) string {
		t.Helper()
		errs, _ := ValidateConfig(decode(t, cfg), clean, nil)
		var mine []string
		for _, e := range errs {
			if strings.Contains(e, "config.provisioners") {
				mine = append(mine, e)
			}
		}
		return strings.Join(mine, "\n")
	}
	for _, ok := range []string{
		`{"provisioners": {"host": ["brew", "nix", "pack"]}}`,
		`{"provisioners": {"host": []}}`,
		`{"provisioners": {"per_package": {"claude": {"host": ["brew", "pack"]}, "rg": {"host": ["nix"]}}}}`,
	} {
		if got := provErrs(ok); got != "" {
			t.Errorf("%s refused:\n%s", ok, got)
		}
	}
	for _, tc := range []struct{ cfg, want string }{
		{`{"provisioners": ["brew"]}`, "config.provisioners: expected an object of environments"},
		{`{"provisioners": {"host": "brew"}}`, "config.provisioners.host: expected a list of provisioners"},
		{`{"provisioners": {"host": ["npm"]}}`, "config.provisioners.host: 'npm' is not a provisioner; the names are pack, apt"},
		{`{"provisioners": {"host": ["brew", "brew"]}}`, `"brew" is named twice`},
		{`{"provisioners": {"jail": ["nix"]}}`, "config.provisioners.jail: no order is read in a jail"},
		{`{"provisioners": {"guest": ["nix"]}}`, "config.provisioners.guest: unknown environment 'guest'"},
		{`{"provisioners": {"per_package": {"claude": ["brew"]}}}`, "config.provisioners.per_package.claude: expected an object of environments"},
		{`{"provisioners": {"per_package": {"psql": {"host": ["brew"]}}}}`, "config.provisioners.per_package.psql: no selected pack declares psql"},
		{`{"provisioners": {"per_package": {"claude": {"host": ["apt"]}}}}`,
			"config.provisioners.per_package.claude.host: no selected pack declares how apt installs claude (its recipes: brew, pack)"},
	} {
		if got := provErrs(tc.cfg); !strings.Contains(got, tc.want) {
			t.Errorf("%s: errors lack %q:\n%s", tc.cfg, tc.want, got)
		}
	}

	// No selected pack ships a nix recipe for a program the floor holds (no shipped agent pack
	// does), so no order naming nix is warned about: rg is a `requires`, which nix supplying is the
	// order working (TestNixAboveThePacksRecipeWarnsOnlyForAFloorProgramWithANixRecipe).
	for _, cfg := range []string{
		`{"provisioners": {"host": ["nix", "pack"]}}`,
		`{"provisioners": {"per_package": {"rg": {"host": ["brew", "nix"]}}}}`,
	} {
		errs, warns := ValidateConfig(decode(t, cfg), clean, nil)
		if joined := strings.Join(warns, "\n"); strings.Contains(joined, "nix is ranked") {
			t.Errorf("%s: warned %q for an order that works as written", cfg, joined)
		}
		if got := strings.Join(errs, "\n"); strings.Contains(got, "config.provisioners") {
			t.Errorf("%s refused: %s", cfg, got)
		}
	}

	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"provisioners": {"host": ["nix"]}}`)
	errs, _ := ValidateConfig(decode(t, `{"provisioners": {"host": ["nix"]}}`), ws, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"config.provisioners: user-scope only", userCfg} {
		if !strings.Contains(joined, want) {
			t.Errorf("a workspace value: errors lack %q:\n%s", want, joined)
		}
	}
}

// TestNixAboveThePacksRecipeWarnsOnlyForAFloorProgramWithANixRecipe: the warning is for an order
// that would give nix a program yolo's host floor holds, which the floor keeps on its own recipe
// until PS-D2's build (hostfloor.Outranking) — a selected `program` with its own recipe AND a nix
// recipe, ranked to nix. It names that program at the order that ranks it: the environment's list,
// or the program's own. Nix below `pack`, a per-package order putting `pack` first, a `requires`
// (rg) and a program with no nix recipe (claude) are the order working, and are not warned about.
func TestNixAboveThePacksRecipeWarnsOnlyForAFloorProgramWithANixRecipe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nixpack")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "pack.json"), `{"name": "nixpack", "contributes": [{"kind": "program", `+
		`"bin": "nixcli", "via": "npm", "package": "nixcli-pkg", "install_hints": {"nix": "nixcli", "brew": "nixcli"}}]}`)
	selectionHome(t, `["claude", "guardrails", "`+root+`"]`)
	clean := t.TempDir()
	for cfg, want := range map[string]string{
		`{"provisioners": {"host": ["nix", "pack"]}}`:                                                                   "config.provisioners.host: nix is ranked above the pack's own recipe for nixcli,",
		`{"provisioners": {"host": ["brew", "nix"]}}`:                                                                   "config.provisioners.host: nix is ranked above the pack's own recipe for nixcli,",
		`{"provisioners": {"per_package": {"nixcli": {"host": ["brew", "nix"]}}}}`:                                      "config.provisioners.per_package.nixcli.host: nix is ranked above the pack's own recipe for nixcli,",
		`{"provisioners": {"host": ["nix"], "per_package": {"nixcli": {"host": ["pack"]}}}}`:                            "",
		`{"provisioners": {"host": ["pack", "nix"]}}`:                                                                   "",
		`{"provisioners": {"per_package": {"rg": {"host": ["nix"]}}}}`:                                                  "",
		`{"provisioners": {"per_package": {"claude": {"host": ["brew", "pack"]}}}}`:                                     "",
		`{"provisioners": {"host": ["brew"]}}`:                                                                          "",
		`{"host_floor": false, "provisioners": {"host": ["nix"]}}`:                                                      "",
		`{"host_floor": {"*": false}, "provisioners": {"host": ["nix"]}}`:                                               "",
		`{"host_floor": {"*": true, "nixpack": false}, "provisioners": {"host": ["nix"]}}`:                              "",
		`{"host_floor": {"*": true, "nixpack": false}, "provisioners": {"per_package": {"nixcli": {"host": ["nix"]}}}}`: "",
		`{"host_floor": {"*": false, "nixpack": true}, "provisioners": {"host": ["nix"]}}`:                              "config.provisioners.host: nix is ranked above the pack's own recipe for nixcli,",
	} {
		errs, warns := ValidateConfig(decode(t, cfg), clean, nil)
		var nix []string
		for _, w := range warns {
			if strings.Contains(w, "nix is ranked") {
				nix = append(nix, w)
			}
		}
		if want == "" && len(nix) != 0 || want != "" && (len(nix) != 1 || !strings.Contains(nix[0], want)) {
			t.Errorf("%s: nix warnings %q, want %q", cfg, nix, want)
		}
		if got := strings.Join(errs, "\n"); strings.Contains(got, "config.provisioners") {
			t.Errorf("%s refused: %s", cfg, got)
		}
	}
}
