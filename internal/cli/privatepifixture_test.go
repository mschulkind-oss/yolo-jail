package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/reporoot"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const privateOfficialPackage = "opencode-ai"
const privateOfficialBin = "opencode"
const privateOfficialRelease = "0.0.0-fixture"

type privateOfficialRecipe struct {
	Package, Bin, Release string
}

var privateOfficialRecipes = []privateOfficialRecipe{
	{privateOfficialPackage, privateOfficialBin, privateOfficialRelease},
	{"@oh-labs/oh-omp", "oh-omp", "0.15.3"},
	{"chrome-devtools-mcp", "chrome-devtools-mcp", privateOfficialRelease},
	{"cowsay", "cowsay", "1.6.0"},
}

func (r privateOfficialRecipe) archive() string {
	if r.Package == privateOfficialPackage {
		return "private-official.tgz"
	}
	return "private-" + r.Bin + ".tgz"
}

func (r privateOfficialRecipe) resolved() string {
	if r.Package == privateOfficialPackage {
		return "https://private.invalid/client.tgz"
	}
	return "https://private.invalid/" + r.Bin + ".tgz"
}

// The initially proposed pi caller cannot use ordinary NPM preparation: its flags
// and refresh retain the established launcher. This fixture uses eligible opencode
// declarations without changing either pack, and has no network client.
func init() {
	if (len(os.Args) == 3 || len(os.Args) == 6) && os.Args[1] == "private-official-setup" {
		if err := privateOfficialSetup(os.Args[2], os.Args[3:]...); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) >= 4 && os.Args[1] == "private-official-npm" {
		if err := privateOfficialNPM(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && (os.Args[1] == "private-official-cli" || os.Args[1] == "internal") {
		marker, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".private-official-producer"))
		if os.IsNotExist(err) {
			if os.Args[1] == "private-official-cli" || filepath.Base(os.Args[0]) == "yolo-private-official" {
				fmt.Fprintln(os.Stderr, "private producer marker missing; ordinary execution refused")
				os.Exit(1)
			}
			return
		}
		if err != nil || !filepath.IsAbs(string(marker)) {
			fmt.Fprintln(os.Stderr, "invalid private producer marker")
			os.Exit(1)
		}
		root := string(marker)
		if err := validatePrivateOfficialRoot(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		self, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) {
			if len(argv) < 2 || argv[0] != "internal" || argv[1] != programCaptureJailVerb {
				return nil, fmt.Errorf("private producer refuses non-program capture child")
			}
			return exec.Command(self, append([]string{"npm-capture-runtime-test", root}, argv...)...), nil
		}
		argv := os.Args
		if argv[1] == "private-official-cli" {
			if len(argv) < 3 || !filepath.IsAbs(argv[2]) {
				fmt.Fprintln(os.Stderr, "private CLI wrapper requires the matched launcher")
				os.Exit(1)
			}
			argv = append([]string{argv[0]}, argv[3:]...)
		}
		os.Exit(Main(argv))
	}
}

func privateOfficialSetup(root string, observed ...string) error {
	if err := os.Chmod(root, 0700); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	tools := filepath.Join(root, "tools")
	if err := os.MkdirAll(tools, 0755); err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	imageIdentity := "private-synthetic-runtime-v1"
	if len(observed) == 3 {
		node, imageIdentity = observed[0], observed[2]
		if !filepath.IsAbs(node) || !filepath.IsAbs(observed[1]) {
			return fmt.Errorf("private producer requires observed physical runtime paths")
		}
		if err := os.WriteFile(filepath.Join(root, "real-npm"), []byte(observed[1]), 0600); err != nil {
			return err
		}
	} else if len(observed) != 0 {
		return fmt.Errorf("private producer requires all three runtime observations")
	}
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(node)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tools, "node"), raw, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "node-digest"), []byte(npmCaptureHash(raw)), 0600); err != nil {
		return err
	}
	for _, recipe := range privateOfficialRecipes {
		tarball, err := privateOfficialRecipeTarball(recipe)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, recipe.archive()), tarball, 0600); err != nil {
			return err
		}
	}
	for name, data := range map[string][]byte{
		"synthetic-ca.pem": []byte("synthetic fixture CA"),
		"image":            []byte(imageIdentity),
	} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
			return err
		}
	}
	for name, command := range map[string]string{
		"npm":  "private-official-npm " + shquote.Quote(root),
		"yolo": "npm-capture-test-image " + shquote.Quote(filepath.Join(root, "image")),
	} {
		script := "#!/bin/sh\nexec " + shquote.Quote(self) + " " + command + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(tools, name), []byte(script), 0755); err != nil {
			return err
		}
	}
	return nil
}

func privateOfficialTarball() ([]byte, error) {
	return privateOfficialRecipeTarball(privateOfficialRecipes[0])
}

func privateOfficialRecipeTarball(recipe privateOfficialRecipe) ([]byte, error) {
	manifest, err := json.Marshal(map[string]any{"name": recipe.Package, "version": recipe.Release, "bin": map[string]string{recipe.Bin: "bin/client.js"}})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, item := range []struct {
		name string
		data []byte
		mode int64
	}{
		{"package/package.json", manifest, 0644},
		{"package/bin/client.js", []byte(fmt.Sprintf("#!/usr/bin/env node\nconsole.log(%q+' '+JSON.stringify(process.argv.slice(2)));\n", "PRIVATE_OFFICIAL "+recipe.Bin+" "+recipe.Release)), 0755},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: item.name, Mode: item.mode, Size: int64(len(item.data)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(item.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func privateOfficialIntegrity(raw []byte) string {
	digest := sha512.Sum512(raw)
	return "sha512-" + base64.StdEncoding.EncodeToString(digest[:])
}

func privateOfficialNPM(root string, args []string) error {
	if len(args) == 1 && args[0] == "--version" {
		actual, err := os.ReadFile(filepath.Join(root, "real-npm"))
		if err == nil {
			// The adapter delegates its version probe to the actual image's npm;
			// it does not forge runtime equality for the consumer.
			cmd := exec.Command(string(actual), "--version")
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		}
		if !os.IsNotExist(err) {
			return err
		}
		fmt.Println("0.0.0-private-fixture")
		return nil
	}
	if len(args) == 0 || (args[0] != "install" && args[0] != "ci") {
		return fmt.Errorf("private producer refuses unexpected npm command")
	}
	prefix := ""
	for i, arg := range args {
		if arg == "--prefix" && i+1 < len(args) {
			prefix = args[i+1]
		}
	}
	if !filepath.IsAbs(prefix) {
		return fmt.Errorf("private producer needs its absolute prefix")
	}
	cfg := os.Getenv("NPM_CONFIG_USERCONFIG")
	info, err := os.Stat(cfg)
	if err != nil || !info.Mode().IsRegular() || info.Size() != 0 || os.Getenv("NPM_CONFIG_GLOBALCONFIG") != "/dev/null" {
		return fmt.Errorf("private producer requires empty isolated npm configuration")
	}
	configs, err := os.OpenFile(filepath.Join(root, "npm-userconfigs"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintln(configs, cfg)
	closeErr := configs.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	manifest, err := os.ReadFile(filepath.Join(prefix, "package.json"))
	if err != nil {
		return err
	}
	var declaration struct{ Dependencies map[string]string }
	if err := json.Unmarshal(manifest, &declaration); err != nil {
		return err
	}
	var recipe privateOfficialRecipe
	for _, candidate := range privateOfficialRecipes {
		selector, ok := declaration.Dependencies[candidate.Package]
		if ok && (selector == candidate.Release || (selector == "latest" && (candidate.Package == privateOfficialPackage || candidate.Package == "chrome-devtools-mcp"))) {
			recipe = candidate
		}
	}
	if recipe.Package == "" || len(declaration.Dependencies) != 1 {
		return fmt.Errorf("private producer refuses unknown package or release")
	}
	raw, err := os.ReadFile(filepath.Join(root, recipe.archive()))
	if err != nil {
		return err
	}
	if args[0] == "install" {
		joined := " " + strings.Join(args, " ") + " "
		if !strings.Contains(joined, " --package-lock-only ") || !strings.Contains(joined, " --ignore-scripts ") {
			return fmt.Errorf("private producer refuses non-resolution install")
		}
		lock, err := json.Marshal(map[string]any{"lockfileVersion": 3, "packages": map[string]any{
			"":                               map[string]any{"dependencies": declaration.Dependencies},
			"node_modules/" + recipe.Package: map[string]any{"version": recipe.Release, "resolved": recipe.resolved(), "integrity": privateOfficialIntegrity(raw), "bin": map[string]string{recipe.Bin: "bin/client.js"}},
		}})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(prefix, "package-lock.json"), lock, 0644); err != nil {
			return err
		}
	} else {
		lockRaw, err := os.ReadFile(filepath.Join(prefix, "package-lock.json"))
		if err != nil {
			return err
		}
		var lock npmCaptureLock
		if err := json.Unmarshal(lockRaw, &lock); err != nil {
			return err
		}
		entry, ok := lock.Packages["node_modules/"+recipe.Package]
		if !ok || len(lock.Packages) != 2 || entry.Version != recipe.Release || entry.Resolved != recipe.resolved() || entry.Integrity != privateOfficialIntegrity(raw) || len(entry.Bin) != 1 || entry.Bin[recipe.Bin] != "bin/client.js" {
			return fmt.Errorf("private producer refuses changed release integrity or metadata")
		}
		expected, err := privateOfficialRecipeTarball(recipe)
		if err != nil || !bytes.Equal(raw, expected) {
			return fmt.Errorf("private producer refuses modified candidate bytes")
		}
		gz, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag != tar.TypeReg || (header.Name != "package/package.json" && header.Name != "package/bin/client.js") {
				return fmt.Errorf("private producer refuses non-fixture tar member")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			name := filepath.Join(prefix, "node_modules", recipe.Package, strings.TrimPrefix(header.Name, "package/"))
			if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(name, data, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
	observed, err := os.OpenFile(filepath.Join(root, "private-execution-events"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, eventErr := fmt.Fprintf(observed, "pid=%d home=%s npm=%s package=%s bin=%s version=%s phase=%s\n", os.Getpid(), os.Getenv("HOME"), filepath.Join(root, "tools", "npm"), recipe.Package, recipe.Bin, recipe.Release, args[0])
	closeEventErr := observed.Close()
	if eventErr != nil {
		return eventErr
	}
	if closeEventErr != nil {
		return closeEventErr
	}
	log, err := os.OpenFile(filepath.Join(root, "private-official-events"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(log, args[0])
	closeErr = log.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func privateOfficialProducer(t *testing.T) (*selectedNPMFixture, run.ProgramCaptureRequest) {
	t.Helper()
	f := newSelectedNPMFixture(t)
	if err := privateOfficialSetup(f.root); err != nil {
		t.Fatal(err)
	}
	f.tools, f.node = filepath.Join(f.root, "tools"), filepath.Join(f.root, "tools", "node")
	t.Setenv("PATH", f.tools+":"+os.Getenv("PATH"))
	npmCaptureNode = privateOfficialNode
	for _, item := range []struct{ key, subdir string }{
		{"XDG_CONFIG_HOME", ".config"}, {"XDG_DATA_HOME", ".local/share"},
		{"XDG_CACHE_HOME", ".cache"}, {"XDG_RUNTIME_DIR", ".run"},
	} {
		t.Setenv(item.key, filepath.Join(os.Getenv("HOME"), item.subdir))
	}
	t.Setenv("YOLO_USER_LAYER", "")
	tarball, err := privateOfficialTarball()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.root, "private-official.tgz"), tarball, 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.tools, "npm"), "#!/bin/sh\nexec "+shquote.Quote(self)+" private-official-npm "+shquote.Quote(f.root)+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(f.tools, "npm"), 0755); err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	source := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, name := range []string{"flake.nix", "flake.lock"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.root, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := resolvePreparationBundle
	resolvePreparationBundle = func() (reporoot.Resolution, bool) { return reporoot.Resolution{Root: f.root}, true }
	t.Cleanup(func() { resolvePreparationBundle = old })
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_SETUP", "podman-self")
	writeFile(t, paths.UserConfigPath(), `{"packs":["opencode"],"agent_updates":false,"host_apply_on_launch":false}`)
	f.store.Dir = paths.CapturesDir()
	selected := selectConfiguredHostPacks()
	if problems := selected.problems(); len(problems) != 0 {
		t.Fatal(problems)
	}
	workspace := t.TempDir()
	cfg, err := config.LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := ordinarySelectionEnvironment(cfg, selected.packs)
	vector, err := selectedOrdinaryInputs(e, selected.packs, entrypoint.ProgramInstallOnly, captureJailPlatform())
	if err != nil || len(vector) != 1 || vector[0].Owner != "opencode" || vector[0].Bin != privateOfficialBin || vector[0].Package != privateOfficialPackage {
		t.Fatalf("unchanged official selection: %+v %v", vector, err)
	}
	f.in = vector[0]
	fingerprint, err := run.ProgramRuntimeFingerprint(f.root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	selectedNPMSharedWorker(t)
	return f, run.ProgramCaptureRequest{Packs: selected.packs, Env: e, Runtime: "podman", Platform: captureJailPlatform(), Fingerprint: fingerprint, RepoRoot: f.root, Workspace: workspace, Act: &run.ActInterrupt{}}
}

func TestPrivateOfficialProducerAdmitsUnchangedOpencodeColdAndFresh(t *testing.T) {
	f, req := privateOfficialProducer(t)
	for _, fresh := range []bool{false, true} {
		req.Fresh, req.Act = fresh, &run.ActInterrupt{}
		got, err := prepareOrdinaryForLaunch(req, io.Discard)
		if err != nil || got[privateOfficialBin].Compatibility == "" {
			t.Fatalf("official inert preparation fresh=%v: %+v %v", fresh, got, err)
		}
		record, err := readNPMCaptureRecord(&capture.Store{Dir: f.store.Dir}, f.in)
		if err != nil || record.Resolution == nil || record.Resolution.Input != f.in || record.Resolution.Release != privateOfficialRelease || record.Fingerprint != req.Fingerprint {
			t.Fatalf("normal producer admission: %+v %v", record, err)
		}
		entry, _, err := resolveProgramCaptureFor(f.store, privateOfficialBin, req.Platform, record.Identity)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := capture.ReadManifest(entry.Root)
		if err != nil || validateProgramManifest(manifest, req.Platform, npmCaptureArtifacts(f.in, record.Identity)) != nil {
			t.Fatalf("normal consumer validation: %v", err)
		}
	}
	if f.resolves.Load() != 2 || f.installs.Load() != 1 {
		t.Fatalf("cold/fresh resolution and immutable installation: %d/%d", f.resolves.Load(), f.installs.Load())
	}
	events, err := os.ReadFile(filepath.Join(f.root, "private-official-events"))
	if err != nil || string(events) != "install\nci\ninstall\n" {
		t.Fatalf("actual private producer events: %q %v", events, err)
	}
	assertNPMCaptureUserConfigsRemoved(t, f.root)
}

func TestPrivateOfficialProducerSelectedPreparationEligibility(t *testing.T) {
	_, req := privateOfficialProducer(t)
	for _, tc := range []struct {
		pack, bin, spec string
	}{
		{"opencode", "opencode", "opencode-ai"},
		{"omp", "oh-omp", "@oh-labs/oh-omp@0.15.3"},
		{"chrome-devtools", "chrome-devtools-mcp", "chrome-devtools-mcp"},
		{"copilot", "", ""},
		{"codex", "", ""},
		{"claude", "", ""},
		{"pi", "", ""},
	} {
		if tc.pack == "omp" && req.Platform != "linux/amd64" {
			tc.bin, tc.spec = "", ""
		}
		t.Run(tc.pack, func(t *testing.T) {
			writeFile(t, paths.UserConfigPath(), `{"packs":["`+tc.pack+`"],"agent_updates":false,"host_apply_on_launch":false}`)
			selected := selectConfiguredHostPacks()
			if problems := selected.problems(); len(problems) != 0 {
				t.Fatal(problems)
			}
			cfg, err := config.LoadConfig(req.Workspace, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			e := ordinarySelectionEnvironment(cfg, selected.packs)
			vector, err := selectedOrdinaryInputs(e, selected.packs, entrypoint.ProgramInstallOnly, req.Platform)
			if err != nil {
				t.Fatal(err)
			}
			if tc.bin == "" {
				if len(vector) != 0 {
					t.Fatalf("no ordinary preparation expected: %+v", vector)
				}
			} else if len(vector) != 1 || vector[0].Owner != tc.pack || vector[0].Bin != tc.bin || vector[0].Package != tc.spec {
				t.Fatalf("official selection vector differs: %+v", vector)
			}
			if tc.pack == "omp" {
				for _, platform := range []string{"linux/amd64", "linux/arm64"} {
					platformVector, err := selectedOrdinaryInputs(e, selected.packs, entrypoint.ProgramInstallOnly, platform)
					if err != nil || (platform == "linux/amd64" && len(platformVector) != 1) || (platform == "linux/arm64" && len(platformVector) != 0) {
						t.Fatalf("official omp platform filter %s: %+v %v", platform, platformVector, err)
					}
				}
			}
			t.Logf("actual ordinary preparation vector: %+v", vector)
		})
	}
}

func TestPrivatePiProducerSelectionKeepsOfficialFlaggedRefreshLauncher(t *testing.T) {
	_, req := privateOfficialProducer(t)
	writeFile(t, paths.UserConfigPath(), `{"packs":["pi"],"agent_updates":false,"host_apply_on_launch":false}`)
	selected := selectConfiguredHostPacks()
	if problems := selected.problems(); len(problems) != 0 {
		t.Fatal(problems)
	}
	cfg, err := config.LoadConfig(req.Workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	vector, err := selectedOrdinaryInputs(ordinarySelectionEnvironment(cfg, selected.packs), selected.packs, entrypoint.ProgramInstallOnly, req.Platform)
	if err != nil || len(vector) != 0 {
		t.Fatalf("official pi must retain its flagged refresh launcher, not host ordinary preparation: %+v %v", vector, err)
	}
	found := false
	for _, p := range selected.packs {
		if p.Name != "pi" {
			continue
		}
		installs, err := p.HonoredInstalls()
		if err != nil || len(installs) != 1 || installs[0].Bin != "pi" || len(installs[0].Flags) == 0 || installs[0].Refresh == nil {
			t.Fatalf("official pi declaration changed: %+v %v", installs, err)
		}
		found = true
	}
	if !found {
		t.Fatal("normal config selection did not select official pi")
	}
}

func TestPrivateOfficialProducerRefusesUnknownPackageAndCorruptIntegrity(t *testing.T) {
	f, req := privateOfficialProducer(t)
	ctx, cancel := context.WithTimeout(context.Background(), testHangGuard(t))
	defer cancel()
	unknown := f.in
	unknown.Package = "unknown-package"
	var unknownOutput bytes.Buffer
	if _, err := resolveNPMCapture(ctx, unknown, t.TempDir(), t.TempDir(), &unknownOutput); err == nil || !strings.Contains(unknownOutput.String(), "private producer refuses unknown package or release") {
		t.Fatalf("unknown package did not fail at the fixture boundary: %v %s", err, &unknownOutput)
	}
	r, err := resolveNPMCapture(ctx, f.in, t.TempDir(), t.TempDir(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	r.Fingerprint = req.Fingerprint
	inputs, err := r.inputs()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := inputs.Identity()
	if err != nil {
		t.Fatal(err)
	}
	// Change the release bytes after resolution: integrity must fail before extraction.
	if err := os.WriteFile(filepath.Join(f.root, "private-official.tgz"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var corruptOutput bytes.Buffer
	if err := installNPMCapture(ctx, *r, identity, home, &corruptOutput); err == nil || !strings.Contains(corruptOutput.String(), "private producer refuses changed release integrity or metadata") {
		t.Fatalf("corrupt release did not fail at integrity validation: %v %s", err, &corruptOutput)
	}
	if events, err := os.ReadFile(filepath.Join(f.root, "private-official-events")); err != nil || string(events) != "install\n" {
		t.Fatalf("negative controls executed an install or fallback: %q %v", events, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".npm-global/bin", privateOfficialBin)); !os.IsNotExist(err) {
		t.Fatalf("failed integrity created executable: %v", err)
	}
}
