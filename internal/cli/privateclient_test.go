package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// auditedSemverURL and auditedSemverIntegrity are the audited cell of docs/design/pack-brokers-http-plan.md's
// Task 2 first slice (programcapturesealedintegration_test.go).

func auditedSemverManifest(pkg string) string {
	return `{"name":"audited-semver","contributes":[{"kind":"program","bin":"semver","via":"npm","package":"` + pkg + `"},` +
		`{"kind":"env","bin":"semver","served_by":"tasks","vars":{"SEMVER_TOKEN":"{caller_token}","SEMVER_URL":"https://{listen}","SSL_CERT_FILE":"{client_trust}"}}]}`
}

// auditedSemverRun prepares the audited-semver pack's program through the actual selected
// producer (prepareOrdinaryForLaunch), as the private client cell cell, with the synthetic
// registry answering with overrides (the npm fixture's resolved/integrity/binmap/binpath files).
func auditedSemverRun(t *testing.T, pkg string, overrides map[string]string, cell func(*packload.PrivateProgramEnvPlan), private bool) (semverRun, map[string]entrypoint.ProgramCaptureDelivery, map[string]callbrokerinput.PreparedHTTPClientEntrypoint, error) {
	t.Helper()
	if runtime.GOOS+"/"+runtime.GOARCH != "linux/amd64" {
		t.Skip("the audited semver cell is linux/amd64 only")
	}
	f := newSelectedNPMFixture(t)
	f.version(t, "7.7.2")
	files := map[string]string{"resolved": auditedSemverURL, "integrity": auditedSemverIntegrity,
		"binmap": `{"semver":"bin/semver.js"}`, "binpath": "bin/semver.js"}
	for k, v := range overrides {
		files[k] = v
	}
	for name, content := range files {
		writeFile(t, filepath.Join(f.root, name), content)
	}
	req := selectedNPMUserPack(t, f, false, "audited-semver", auditedSemverManifest(pkg))
	if f.in.Owner != "audited-semver" || f.in.Bin != "semver" || f.in.Package != pkg {
		t.Fatalf("selected ordinary input is not the audited cell: %+v", f.in)
	}
	selectedNPMSharedWorker(t)
	var inst *packload.PrivateProgramEnvPlan
	for _, p := range req.Packs {
		if p.Name != "audited-semver" {
			continue
		}
		for _, c := range p.Decl.InstallContributions() {
			if c.Bin != "semver" {
				continue
			}
			identity, err := packload.ProgramRecipeIdentity(c)
			if err != nil {
				t.Fatal(err)
			}
			inst = &packload.PrivateProgramEnvPlan{Bin: "semver", Pack: "audited-semver", Front: "tasks", RecipePack: "audited-semver", RecipeIdentity: identity}
		}
	}
	if inst == nil {
		t.Fatal("selected audited-semver pack declares no semver program")
	}
	if cell != nil {
		cell(inst)
	}
	if private {
		req.PrivateClients = []packload.PrivateProgramEnvPlan{*inst}
		req.PrivateClientEntrypoints = map[string]callbrokerinput.PreparedHTTPClientEntrypoint{}
	}
	got, err := prepareOrdinaryForLaunch(req, io.Discard)
	return semverRun{f: f, cell: *inst}, got, req.PrivateClientEntrypoints, err
}

type semverRun struct {
	f    *selectedNPMFixture
	cell packload.PrivateProgramEnvPlan
}

func TestAuditedSemverPrivateClientIsVerifiedByTheSelectedProducer(t *testing.T) {
	r, deliveries, checked, err := auditedSemverRun(t, "semver@7.7.2", nil, nil, true)
	if err != nil {
		t.Fatalf("the audited cell was refused: %v", err)
	}
	delivery, ok := deliveries["semver"]
	c, okChecked := checked["semver"]
	if !ok || !okChecked {
		t.Fatalf("producer delivered %v and checked %v", deliveries, checked)
	}
	if !c.SameSelection(callbrokerinput.PreparedHTTPClientPlan{Bin: r.cell.Bin, Pack: r.cell.Pack, Front: r.cell.Front, RecipePack: r.cell.RecipePack, RecipeIdentity: r.cell.RecipeIdentity}) {
		t.Fatalf("checked entrypoint names another selection: %+v", c)
	}
	if c.Declared != "semver@7.7.2" || c.Release != "7.7.2" || c.SourceURL != auditedSemverURL || c.SourceIntegrity != auditedSemverIntegrity ||
		c.Platform != "linux/amd64" || c.Compatibility != delivery.Compatibility || c.Runtime != delivery.Runtime || c.Bootstrap != entrypoint.PrivateClientBootstrap {
		t.Fatalf("checked delivery does not bind the audited cell: %+v (delivery %+v)", c, delivery)
	}
	if err := c.ValidateDelivery(); err != nil || c.Binding.Generation != "" {
		t.Fatalf("checked delivery invalid or pre-bound: %v %+v", err, c.Binding)
	}
	if want := ".local/lib/yolo-npm/" + delivery.Compatibility + "/node_modules/semver/bin/semver.js"; c.Binding.Entrypoint != want {
		t.Fatalf("entrypoint %q, want %q", c.Binding.Entrypoint, want)
	}
	// The binding names the admitted capture entry's own bytes: the bootstrap's check passes on it,
	// and a changed entrypoint fails it.
	b := c.Binding
	b.Generation = strings.Repeat("a", 32)
	abs, err := entrypoint.VerifyPrivateClient(paths.CapturesDir(), b)
	if err != nil {
		t.Fatalf("the bootstrap's check refuses the producer's binding: %v", err)
	}
	if abs != filepath.Join(paths.CapturesDir(), "entries", b.CaptureKey, "tree", filepath.FromSlash(b.Entrypoint)) {
		t.Fatalf("verified entrypoint %q is not in the admitted entry", abs)
	}
	entry := filepath.Join(capture.TreeDir(filepath.Join(paths.CapturesDir(), "entries", b.CaptureKey)), filepath.FromSlash(b.Entrypoint))
	if err := os.Chmod(entry, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := entrypoint.VerifyPrivateClient(paths.CapturesDir(), b); err == nil {
		t.Fatal("a changed captured entrypoint still verified")
	}
}

func TestAuditedSemverPrivateClientRefusesMismatchedDeliveries(t *testing.T) {
	for _, tc := range []struct {
		name      string
		pkg       string
		overrides map[string]string
		cell      func(*packload.PrivateProgramEnvPlan)
		cause     string
		// legacy is whether the same delivery, for a program that is not a private client, keeps
		// the ordinary path (true) or is refused for every program (false).
		legacy bool
	}{
		{name: "same-key recipe", pkg: "semver@7.7.2", cell: func(c *packload.PrivateProgramEnvPlan) { c.RecipeIdentity = "sha256:" + strings.Repeat("b", 64) },
			cause: "prepared recipe is not the selected one", legacy: true},
		{name: "unselected recipe pack", pkg: "semver@7.7.2", cell: func(c *packload.PrivateProgramEnvPlan) { c.RecipePack = "other-pack" },
			cause: "prepared program is audited-semver/semver", legacy: true},
		{name: "unpinned selector", pkg: "semver", cause: "pins no exact audited release", legacy: true},
		{name: "registry fork tarball", pkg: "semver@7.7.2", overrides: map[string]string{"resolved": "https://registry.npmjs.org/semver-fork/-/semver-fork-7.7.2.tgz"},
			cause: "not the registry's audited tarball", legacy: true},
		{name: "weak integrity", pkg: "semver@7.7.2", overrides: map[string]string{"integrity": "sha1-abc"}, cause: "SHA-512", legacy: true},
		{name: "git source", pkg: "semver@7.7.2", overrides: map[string]string{"resolved": "git+https://example.invalid/semver.git#fixed"},
			cause: "not the registry's audited tarball", legacy: true},
		{name: "unlocked dependency graph", pkg: "semver@7.7.2", overrides: map[string]string{"unsupported-lock": "1"},
			cause: "its delivery is unsupported", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, checked, err := auditedSemverRun(t, tc.pkg, tc.overrides, tc.cell, true)
			var refusal *privateClientDeliveryRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("a mismatched private delivery was not refused: %v (checked %+v)", err, checked)
			}
			out := refusal.Render(false)
			for _, want := range []string{"semver", "audited-semver", "tasks", tc.cause, "Repair:", "Nothing falls back"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal does not name %q:\n%s", want, out)
				}
			}
			if len(checked) != 0 {
				t.Fatalf("a refused delivery was still checked: %+v", checked)
			}
			if !tc.legacy {
				return
			}
			// Control: the ordinary program path for a program that is no private client is unchanged.
			_, _, _, err = auditedSemverRun(t, tc.pkg, tc.overrides, nil, false)
			if errors.As(err, &refusal) {
				t.Fatalf("an ordinary program was refused as a private client: %v", err)
			}
		})
	}
}

func TestPrivateClientCellsRefuseAnUnsupportedRuntimeBeforePreparation(t *testing.T) {
	cell := packload.PrivateProgramEnvPlan{Bin: "semver", Pack: "audited-semver", Front: "tasks", RecipePack: "audited-semver", RecipeIdentity: "sha256:" + strings.Repeat("a", 64)}
	for name, req := range map[string]func() (map[string]callbrokerinput.PreparedHTTPClientEntrypoint, string){
		"customized runtime": func() (map[string]callbrokerinput.PreparedHTTPClientEntrypoint, string) {
			return map[string]callbrokerinput.PreparedHTTPClientEntrypoint{}, "extra packages"
		},
		"no place for the checked delivery": func() (map[string]callbrokerinput.PreparedHTTPClientEntrypoint, string) { return nil, "" },
	} {
		t.Run(name, func(t *testing.T) {
			checked, unsupported := req()
			_, err := prepareOrdinaryForLaunch(runProgramCaptureRequest(cell, checked, unsupported), io.Discard)
			var refusal *privateClientDeliveryRefusal
			if !errors.As(err, &refusal) || refusal.Cell != cell || !strings.Contains(refusal.Render(false), "Repair:") {
				t.Fatalf("an unsupported private client cell was not refused: %v", err)
			}
		})
	}
}

func runProgramCaptureRequest(cell packload.PrivateProgramEnvPlan, checked map[string]callbrokerinput.PreparedHTTPClientEntrypoint, unsupported string) run.ProgramCaptureRequest {
	return run.ProgramCaptureRequest{Runtime: "podman", Platform: "linux/amd64", UnsupportedRuntime: unsupported, Act: &run.ActInterrupt{},
		PrivateClients: []packload.PrivateProgramEnvPlan{cell}, PrivateClientEntrypoints: checked}
}

func TestPrivateClientBootstrapExecsOnlyTheBoundVerifiedEntrypoint(t *testing.T) {
	_, _, checked, err := auditedSemverRun(t, "semver@7.7.2", nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	b := checked["semver"].Binding
	b.Generation = strings.Repeat("a", 32)
	env, err := entrypoint.MarshalPrivateClients([]entrypoint.PrivateClientBinding{b})
	if err != nil {
		t.Fatal(err)
	}
	nodeDir := t.TempDir()
	writeFile(t, filepath.Join(nodeDir, "node"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(nodeDir, "node"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", nodeDir)
	oldExec, oldStore := privateClientExec, privateClientStore
	t.Cleanup(func() { privateClientExec, privateClientStore = oldExec, oldStore })
	privateClientStore = paths.CapturesDir()
	var execed []string
	privateClientExec = func(argv0 string, argv []string, _ []string) error {
		execed = append([]string{argv0}, argv...)
		return nil
	}
	args := append(entrypoint.PrivateClientArgv(b)[3:], "1.2.3", "-r", ">1")
	if entrypoint.PrivateClientArgv(b)[0] != entrypoint.PrivateClientBootstrap || entrypoint.PrivateClientArgv(b)[2] != entrypoint.PrivateClientExecVerb {
		t.Fatalf("bound argv is not the shipped bootstrap's: %q", entrypoint.PrivateClientArgv(b))
	}
	t.Setenv(entrypoint.PrivateHTTPClientsEnv, env)
	// Through the `yolo internal` dispatcher the bound argv names.
	if rc := runInternal(append([]string{entrypoint.PrivateClientExecVerb}, args...)); rc != 0 {
		t.Fatalf("bound bootstrap refused: rc=%d", rc)
	}
	want := filepath.Join(paths.CapturesDir(), "entries", b.CaptureKey, "tree", filepath.FromSlash(b.Entrypoint))
	if len(execed) != 6 || execed[0] != filepath.Join(nodeDir, "node") || execed[2] != want || strings.Join(execed[3:], " ") != "1.2.3 -r >1" {
		t.Fatalf("bootstrap exec'd %q", execed)
	}
	other := b
	other.Generation = strings.Repeat("b", 32)
	otherEnv, _ := entrypoint.MarshalPrivateClients([]entrypoint.PrivateClientBinding{other})
	for name, tc := range map[string]struct {
		args []string
		env  string
	}{
		"no jail binding":    {args, ""},
		"another generation": {args, otherEnv},
		"tampered entrypoint": {append(entrypoint.PrivateClientArgv(func() entrypoint.PrivateClientBinding {
			c := b
			c.EntrypointSHA256 = "sha256:" + strings.Repeat("0", 64)
			return c
		}())[3:], "x"), env},
		"reordered arguments": {append([]string{args[1], args[0]}, args[2:]...), env},
		"missing separator":   {entrypoint.PrivateClientArgv(b)[3 : len(entrypoint.PrivateClientArgv(b))-1], env},
	} {
		t.Run(name, func(t *testing.T) {
			execed = nil
			if rc := runPrivateClientExec(tc.args, func(k string) string {
				if k == entrypoint.PrivateHTTPClientsEnv {
					return tc.env
				}
				return ""
			}); rc == 0 || execed != nil {
				t.Fatalf("bootstrap ran an unbound or tampered client: rc=%d exec=%q", rc, execed)
			}
		})
	}
}
