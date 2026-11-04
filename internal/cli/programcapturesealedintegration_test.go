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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"debug/buildinfo"
	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"reflect"
	"runtime/debug"
)

// This exact tarball's entire 52-file source was audited: local semver mathematics,
// static relative requires, console/argv only; no runtime/optional/bundled deps,
// install lifecycle scripts, binding.gyp or native binaries. Mutable metadata is
// never installation authority here: both fetched bytes and the real resolver lock
// must match this pre-audited digest before the unchanged npm ci producer runs.
const auditedSemverIntegrity = "sha512-RF0Fw+rO5AMf9MAyaRXI4AV0Ulj5lMHqVxxdSgiVbixSCXoEmmX/jk0CuJw4+3SqroYO9VoUh+HcuJivvtJemA=="
const auditedSemverURL = "https://registry.npmjs.org/semver/-/semver-7.7.2.tgz"

func auditSealedSemverTarball(raw []byte) error {
	digest := sha512.Sum512(raw)
	if "sha512-"+base64.StdEncoding.EncodeToString(digest[:]) != auditedSemverIntegrity {
		return fmt.Errorf("unaudited semver tarball digest")
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	count := 0
	var manifest []byte
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !strings.HasPrefix(h.Name, "package/") || !filepath.IsLocal(h.Name) || strings.Contains(h.Name, "\\") || filepath.Base(h.Name) == "binding.gyp" {
			return fmt.Errorf("unsafe tarball entry %q", h.Name)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		count++
		if h.Name == "package/package.json" {
			manifest = b
		}
	}
	var p struct {
		Name, Version                                        string
		Bin                                                  map[string]string
		Scripts                                              map[string]string
		Dependencies, OptionalDependencies, PeerDependencies map[string]any
		BundleDependencies, BundledDependencies              json.RawMessage
	}
	if err := json.Unmarshal(manifest, &p); err != nil {
		return err
	}
	if count != 52 || p.Name != "semver" || p.Version != "7.7.2" || len(p.Bin) != 1 || p.Bin["semver"] != "bin/semver.js" || len(p.Dependencies)+len(p.OptionalDependencies)+len(p.PeerDependencies) != 0 || len(p.BundleDependencies)+len(p.BundledDependencies) != 0 {
		return fmt.Errorf("unaudited package graph or bin")
	}
	for _, hook := range []string{"preinstall", "install", "postinstall", "prepublish", "preprepare", "prepare", "postprepare", "prepack", "postpack"} {
		if _, ok := p.Scripts[hook]; ok {
			return fmt.Errorf("unaudited lifecycle %s", hook)
		}
	}
	return nil
}

func auditSealedSemverResolution(raw []byte) error {
	var r npmCaptureResolution
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if r.Input.Package != "semver@7.7.2" || r.Input.Bin != "semver" || r.Release != "7.7.2" || r.Source != auditedSemverURL+"|"+auditedSemverIntegrity || r.BinPath != "bin/semver.js" || r.Runtime.TrustStore != npmCaptureTrustStore {
		return fmt.Errorf("resolver changed audited source")
	}
	if _, err := r.inputs(); err != nil {
		return err
	}
	var complete struct{ Packages map[string]json.RawMessage }
	if err := json.Unmarshal(r.Lock, &complete); err != nil {
		return err
	}
	if err := auditSealedRootManifest(complete.Packages[""]); err != nil {
		return err
	}
	var lock struct {
		LockfileVersion int
		Packages        map[string]struct {
			Version, Resolved, Integrity                         string
			Bin                                                  map[string]string
			Dependencies, OptionalDependencies, PeerDependencies map[string]any
			HasInstallScript, Link                               bool
		}
	}
	if err := json.Unmarshal(r.Lock, &lock); err != nil {
		return err
	}
	if lock.LockfileVersion != 3 || len(lock.Packages) != 2 {
		return fmt.Errorf("unaudited resolver graph")
	}
	declaration, present := lock.Packages[""]
	if !present || len(declaration.Dependencies) != 1 || declaration.Dependencies["semver"] != "7.7.2" || len(declaration.OptionalDependencies)+len(declaration.PeerDependencies) != 0 || declaration.HasInstallScript || declaration.Link {
		return fmt.Errorf("unaudited root declaration")
	}
	root, ok := lock.Packages["node_modules/semver"]
	if !ok || root.Version != "7.7.2" || root.Resolved != auditedSemverURL || root.Integrity != auditedSemverIntegrity || root.HasInstallScript || root.Link || len(root.Dependencies)+len(root.OptionalDependencies)+len(root.PeerDependencies) != 0 || len(root.Bin) != 1 || root.Bin["semver"] != "bin/semver.js" {
		return fmt.Errorf("unaudited resolver package")
	}
	return nil
}

// Explicit opt-in only, never under -short. Requires Linux Podman and a matched
// candidate launcher paired with its flake/helper bundle. Each child is owned
// and bounded by the existing lifecycle; the whole test requires a <=5m deadline.
// Uses anonymous verified TLS with only the immutable image CA bundle. No agents,
// models, host services or fetched hooks run. The launch callback is the existing
// public seam; after its real preparation, a real candidate CLI consumes the archive.
func TestAuditedRegistrySealedPublicColdWarmAndFreshRefusal(t *testing.T) {
	if testing.Short() || os.Getenv("YOLO_TEST_AUDITED_REGISTRY") != "1" {
		t.Skip("explicit audited registry runtime opt-in; not a short test")
	}
	if runtime.GOOS != "linux" {
		t.Skip("Linux Podman proof")
	}
	deadline, ok := t.Deadline()
	if !ok || time.Until(deadline) > 5*time.Minute {
		t.Fatal("run with -timeout <=5m")
	}
	root := os.Getenv("YOLO_TEST_SOURCE_ROOT")
	if !filepath.IsAbs(root) {
		t.Fatal("absolute committed build-input source root required in YOLO_TEST_SOURCE_ROOT")
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("Podman unavailable")
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.Add(-10*time.Second))
	defer cancel()
	owned, err := os.MkdirTemp("", "audited-sealed-registry-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("retained owned evidence", owned)
	exe := buildAuditedSealedCandidate(t, ctx, root, owned)
	bundleRoot := filepath.Join(filepath.Dir(filepath.Dir(exe)), "share", "yolo-jail")
	for key, sub := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "cache", "XDG_RUNTIME_DIR": "runtime"} {
		dir := filepath.Join(owned, sub)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	// Neither the public consumer nor the proof process inherits the live jail's
	// service endpoints, pack tree, provider table or workspace identities.
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "YOLO_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("YOLO_SETUP", "podman-self")
	t.Setenv("NO_COLOR", "1")
	// Discard caller trust/proxy/credential knobs, not the shared Go build cache.
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "NPM_") || strings.HasPrefix(upper, "NODE_") || strings.HasSuffix(upper, "_PROXY") || upper == "SSL_CERT_FILE" || upper == "SSL_CERT_DIR" || upper == "NIX_SSL_CERT_FILE" {
			t.Setenv(key, "")
		}
	}
	writeFile(t, paths.UserConfigPath(), `{}`)
	auditDir := filepath.Join(owned, "audit")
	if err := os.Mkdir(auditDir, 0700); err != nil {
		t.Fatal(err)
	}
	fetch := `const https=require('https'),fs=require('fs');(async()=>{for(const [file,url] of [['metadata.json','https://registry.npmjs.org/semver/7.7.2'],['semver.tgz','` + auditedSemverURL + `']]){await new Promise((resolve,reject)=>{https.get(url,r=>{if(r.statusCode!==200){r.resume();reject(Error('status '+r.statusCode));return;}const f=fs.createWriteStream('/workspace/'+file);r.pipe(f);f.on('finish',()=>{f.close();console.log('AUDIT_FETCH',file,r.statusCode);resolve();});f.on('error',reject);}).on('error',reject);});}})().catch(e=>{console.error(e);process.exitCode=1});`
	writeFile(t, filepath.Join(auditDir, "fetch.js"), fetch)
	var log auditedSealedLog
	cmd := exec.CommandContext(ctx, exe, "internal", programCaptureJailVerb, "--workspace="+auditDir, "--", "bash", "-c", `env -i HOME="$HOME" PATH="$PATH" SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt SSL_CERT_DIR= NODE_OPTIONS=--use-openssl-ca node /workspace/fetch.js`)
	cmd.Env = programCaptureEnvironment(os.Environ())
	cmd.Stdout = &log
	cmd.Stderr = &log
	if err := cmd.Run(); err != nil {
		t.Fatalf("sealed audit fetch: %v\n%s", err, log.String())
	}
	raw, err := os.ReadFile(filepath.Join(auditDir, "semver.tgz"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auditSealedSemverTarball(raw); err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(auditDir, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Dist struct{ Integrity, Tarball string }
	}
	if err := json.Unmarshal(metadata, &meta); err != nil || meta.Dist.Integrity != auditedSemverIntegrity || meta.Dist.Tarball != auditedSemverURL {
		t.Fatal("metadata disagrees with immutable audited bytes", err)
	}
	pack := filepath.Join(owned, "pack")
	workspace := filepath.Join(owned, "consumer")
	for _, p := range []string{pack, workspace} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"audited-semver","contributes":[{"kind":"program","bin":"semver","via":"npm","package":"semver@7.7.2"}]}`)
	cfgJSON, _ := json.Marshal(map[string]any{"packs": []any{map[string]string{"source": pack, "name": "audited-semver"}}, "agent_updates": false})
	writeFile(t, paths.UserConfigPath(), string(cfgJSON))
	selected := selectConfiguredHostPacks()
	if len(selected.problems()) != 0 {
		t.Fatal(selected.problems())
	}
	cfg, err := config.LoadConfig(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := run.ProgramRuntimeFingerprint(bundleRoot, cfg)
	if err != nil {
		t.Fatal(err)
	}
	req := run.ProgramCaptureRequest{Packs: selected.packs, Env: ordinarySelectionEnvironment(cfg, selected.packs), Runtime: "podman", Platform: captureJailPlatform(), Fingerprint: fingerprint, RepoRoot: bundleRoot, Workspace: workspace}
	store := &capture.Store{Dir: paths.CapturesDir()}
	in := npmCaptureInput{Owner: "audited-semver", Bin: "semver", Package: "semver@7.7.2"}
	originalChild, originalCommand, originalSpawn, originalLaunch := programCaptureChild, forkBuildChildCommand, sharedPreparationSpawn, launchRunPipeline
	var workers sync.WaitGroup
	t.Cleanup(func() {
		workers.Wait()
		programCaptureChild = originalChild
		forkBuildChildCommand = originalCommand
		sharedPreparationSpawn = originalSpawn
		launchRunPipeline = originalLaunch
		writeFile(t, filepath.Join(owned, "runtime.log"), log.String())
	})
	forkBuildChildCommand = func(argv []string) (*exec.Cmd, error) { return exec.Command(exe, argv...), nil }
	sharedPreparationSpawn = func(_ context.Context, d preparationDemand) error {
		workers.Add(1)
		go func() { defer workers.Done(); _ = runSharedPreparation(ctx, d, &log) }()
		return nil
	}
	var resolves, installs atomic.Int32
	var denyFresh atomic.Bool
	programCaptureChild = func(childCtx context.Context, staging string, argv []string, w io.Writer) error {
		isResolve := len(argv) > 3 && argv[2] == "program-npm" && argv[3] == "resolve"
		var err error
		if !isResolve {
			b, readErr := os.ReadFile(filepath.Join(staging, "resolution.json"))
			if readErr != nil {
				return readErr
			}
			err = auditedSealedResolutionGate(b, func() error {
				if err := auditSealedSemverTarball(raw); err != nil {
					return err
				}
				installs.Add(1)
				return originalChild(childCtx, staging, argv, w)
			})
		} else {
			err = originalChild(childCtx, staging, argv, w)
		}
		if err != nil {
			return err
		}
		if isResolve {
			resolves.Add(1)
			b, err := os.ReadFile(filepath.Join(staging, "resolution.json"))
			if err != nil {
				return err
			}
			if err := auditSealedSemverResolution(b); err != nil {
				return err
			}
			if denyFresh.Load() {
				return fmt.Errorf("controlled fresh audit refusal — retry preparation")
			}
		}
		return nil
	}
	consumerHome := filepath.Join(owned, "consumer-home")
	if err := os.Mkdir(consumerHome, 0700); err != nil {
		t.Fatal(err)
	}
	consumerRuns := 0
	launchRunPipeline = func(o run.Options) int {
		if o.PreparePrograms == nil {
			t.Error("public callback missing")
			return 1
		}
		req.Fresh = o.Update
		req.Act = &run.ActInterrupt{}
		req.Stderr = &log
		deliveries, err := o.PreparePrograms(req)
		workers.Wait()
		if err != nil {
			fmt.Fprintln(&log, "PUBLIC_REFUSAL", err)
			if len(deliveries) != 0 {
				t.Error("failed fresh check returned stale delivery")
			}
			return 1
		}
		delivery, ok := deliveries["semver"]
		if !ok {
			t.Error("no real capture delivery")
			return 1
		}
		record, err := readNPMCaptureRecord(store, in)
		exact, deliveryErr := ordinaryCaptureDelivery(record)
		if err != nil || deliveryErr != nil || exact != delivery || record.Identity != delivery.Compatibility || record.Entry == "" {
			t.Error("delivery lacks actual admitted record")
			return 1
		}
		e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": consumerHome, "YOLO_WORKSPACE": workspace, entrypoint.CapturesDirEnv: store.Dir, entrypoint.ProgramCapturesEnv: entrypoint.MarshalProgramCaptures(deliveries)})
		// Existing MCP archive-consumer seam; its Run callback refuses ANY legacy
		// npm/producer command before spawning it. No second run pipeline can install.
		// The outer session can have another mise Node ahead of the baseline.
		// Positively observe the actual image Node and require full production
		// runtime equality before materialization; never infer equivalence.
		observed, observeErr := observeNPMCaptureRuntime(ctx, consumerHome, "/bin/node")
		if observeErr != nil || observed != record.Resolution.Runtime {
			t.Error("image Node does not match admitted runtime", observeErr)
			return 1
		}
		consumerEnv := append(programCaptureEnvironment(os.Environ()), "PATH=/bin:/usr/bin")
		err = auditedSealedRefresh(ctx, e, delivery, func(argv []string) error {
			command := exec.CommandContext(ctx, exe, argv[1:]...)
			command.Env = consumerEnv
			output, err := command.CombinedOutput()
			_, _ = log.Write(output)
			return err
		})
		if err != nil {
			fmt.Fprintln(&log, "CONSUMER_REFUSAL", err)
			t.Error("archive consumer refused; private log retained")
			return 1
		}
		if err := auditedSealedMaterializationWitness(e, record); err != nil {
			t.Error(err)
			return 1
		}
		consumerRuns++
		command := exec.CommandContext(ctx, filepath.Join(e.NpmPrefix, "bin/semver"), "-r", "^1.0.0", "1.2.3", "2.0.0")
		command.Env = consumerEnv
		output, err := command.CombinedOutput()
		fmt.Fprintf(&log, "CONSUMER %d %s", consumerRuns, output)
		if err != nil || string(output) != "1.2.3\n" {
			t.Error("audited local semver output mismatch")
			return 1
		}
		return 0
	}
	call := func(label string, fresh bool, wantRC int) {
		t.Helper()
		start := time.Now()
		args := []string{"run"}
		if fresh {
			args = append(args, "--update")
		}
		args = append(args, "--", "semver", "1.2.3")
		rc := runRun(args)
		t.Logf("%s rc=%d elapsed=%s resolve=%d install=%d consumers=%d", label, rc, time.Since(start), resolves.Load(), installs.Load(), consumerRuns)
		if rc != wantRC {
			t.Fatalf("%s status %d, want %d\n%s", label, rc, wantRC, log.String())
		}
	}
	call("cold", false, 0)
	first, err := readNPMCaptureRecord(store, in)
	if err != nil || first.Entry == "" || first.Completed != 1 || resolves.Load() != 1 || installs.Load() != 1 {
		t.Fatalf("cold not admitted: %+v %v", first, err)
	}
	if err := auditSealedSemverResolution(auditedSealedJSON(first.Resolution)); err != nil {
		t.Fatal(err)
	}
	t.Logf("runtime=%+v identity=%s entry=%s", first.Resolution.Runtime, first.Identity, first.Entry)
	entryRoot := store.EntryDir(first.Entry)
	manifest, err := capture.ReadManifest(entryRoot)
	if err != nil || manifest.RefScan != capture.RefScanFull || !manifest.Relocatable {
		t.Fatalf("capture witness: %+v %v", manifest, err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		want, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(entryRoot, "tree", ".local/lib/yolo-npm", first.Identity, "node_modules/semver", strings.TrimPrefix(h.Name, "package/"))
		actual, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(actual, want) {
			t.Fatalf("installed source differs from audited tarball: %s %v", h.Name, err)
		}
	}
	t.Logf("artifact manifest entries=%d bytes=%d scan=%s relocatable=%t; all 52 installed files match audited tarball", len(manifest.Entries), manifest.TotalBytes(), manifest.RefScan, manifest.Relocatable)
	call("warm", false, 0)
	if resolves.Load() != 1 || installs.Load() != 1 {
		t.Fatal("warm contacted producer")
	}
	call("fixed-fresh", true, 0)
	fixed, err := readNPMCaptureRecord(store, in)
	if err != nil || fixed.Identity != first.Identity || fixed.Entry != first.Entry || fixed.Completed != 2 || resolves.Load() != 2 || installs.Load() != 1 {
		t.Fatalf("fixed selection changed or reinstalled: %+v %v", fixed, err)
	}
	denyFresh.Store(true)
	call("fresh-refusal", true, 1)
	failed, err := readNPMCaptureRecord(store, in)
	if err != nil || failed.Completed != fixed.Completed || failed.Started <= failed.Completed || failed.Failure == "" || failed.Entry != fixed.Entry || failed.Identity != fixed.Identity || failed.Fingerprint != fixed.Fingerprint || !reflect.DeepEqual(failed.Resolution, fixed.Resolution) || installs.Load() != 1 || consumerRuns != 3 {
		t.Fatalf("fresh failure served staleGood or published: %+v %v", failed, err)
	}
	good, receipt, err := resolveProgramCaptureFor(store, in.Bin, req.Platform, fixed.Identity)
	if err != nil || good.Key != fixed.Entry || receipt.Compatibility != fixed.Identity {
		t.Fatal("refusal changed actual selected capture Good", err)
	}
	current, err := selectedOrdinaryInputs(ordinarySelectionEnvironment(cfg, selected.packs), selected.packs, entrypoint.ProgramInstallOnly, captureJailPlatform())
	if err != nil || len(current) != 1 || current[0] != in || failed.Resolution.Release != "7.7.2" {
		t.Fatal("refusal changed fixed declaration/release pin", err)
	}
	t.Log("AUDITED_SUCCESS cold/warm/fixed/fresh-refusal; no stale delivery or target on failed fresh check")
}

func auditedSealedJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

type auditedSealedLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *auditedSealedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *auditedSealedLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// Pure safety fixtures never resolve, install, admit or execute their injected data.
func TestAuditedRegistryRootManifestRejectsInjectedInstallAuthority(t *testing.T) {
	base := map[string]any{"dependencies": map[string]any{"semver": "7.7.2"}}
	for _, field := range []string{"scripts", "optionalDependencies", "peerDependencies", "peerDependenciesMeta", "bundledDependencies", "bundleDependencies", "devDependencies", "workspaces", "overrides", "resolutions", "config", "os", "cpu", "engines", "bin", "gypfile", "hasInstallScript", "unknownInstallAuthority"} {
		t.Run(field, func(t *testing.T) {
			root := map[string]any{}
			for k, v := range base {
				root[k] = v
			}
			root[field] = map[string]any{"preinstall": "echo must-not-run"}
			calls := 0
			err := auditedSealedResolutionGate(auditedSealedJSON(auditedSealedResolutionFixture(root)), func() error { calls++; return nil })
			if err == nil || calls != 0 {
				t.Fatalf("injected root %s reached child: calls=%d err=%v", field, calls, err)
			}
		})
	}
}

func auditedSealedResolutionFixture(root map[string]any) npmCaptureResolution {
	lock := map[string]any{"lockfileVersion": 3, "packages": map[string]any{"": root, "node_modules/semver": map[string]any{"version": "7.7.2", "resolved": auditedSemverURL, "integrity": auditedSemverIntegrity, "bin": map[string]string{"semver": "bin/semver.js"}}}}
	return npmCaptureResolution{Input: npmCaptureInput{Bin: "semver", Package: "semver@7.7.2"}, Runtime: npmCaptureRuntime{Image: "fixture", Node: "fixture", NodeDigest: "fixture", NPM: "fixture", Platform: "linux/amd64", ABI: "fixture", TrustStore: npmCaptureTrustStore}, Lock: auditedSealedJSON(lock), Release: "7.7.2", Source: auditedSemverURL + "|" + auditedSemverIntegrity, BinPath: "bin/semver.js", Dependencies: []string{"node_modules/semver=7.7.2|" + auditedSemverURL + "|" + auditedSemverIntegrity}}
}

func auditSealedRootManifest(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	for field, value := range root {
		switch field {
		case "dependencies":
			var deps map[string]string
			if err := json.Unmarshal(value, &deps); err != nil || len(deps) != 1 || deps["semver"] != "7.7.2" {
				return fmt.Errorf("unaudited root dependencies")
			}
		case "name", "version", "license":
			var text string
			if err := json.Unmarshal(value, &text); err != nil {
				return fmt.Errorf("invalid root metadata %s", field)
			}
		case "private":
			var private bool
			if err := json.Unmarshal(value, &private); err != nil || !private {
				return fmt.Errorf("invalid root private flag")
			}
		default:
			return fmt.Errorf("unaudited root manifest field %s", field)
		}
	}
	if _, ok := root["dependencies"]; !ok {
		return fmt.Errorf("root dependencies absent")
	}
	return nil
}

func auditedSealedRefresh(ctx context.Context, e *entrypoint.Env, d entrypoint.ProgramCaptureDelivery, next func([]string) error) error {
	return entrypoint.RefreshServers(entrypoint.ServerRefreshRequest{Context: ctx, Env: e, NpmSpecs: "semver@7.7.2", Updates: true, Stderr: io.Discard, Run: func(_ context.Context, _ io.Writer, argv []string) error {
		// A deleted archive callsite reaches this gate as npm install and is
		// refused before execution; it can never produce a plausible bin output.
		want := []string{"yolo", "internal", "capture-materialize", "--store=" + e.Getenv(entrypoint.CapturesDirEnv), "--bin=semver", "--home=" + e.Home, "--declared=semver@7.7.2", "--compatibility=" + d.Compatibility, "--receipts=" + filepath.Join(e.Workspace, ".yolo/receipts.jsonl")}
		if d.Runtime != "" {
			want = append(want, "--runtime="+d.Runtime, "--node-floor="+d.NodeFloor)
		}
		if len(argv) == len(want)+1 && argv[len(argv)-1] == "--validate-only" {
			want = append(want, "--validate-only")
		}
		if !reflect.DeepEqual(argv, want) {
			return fmt.Errorf("consumer attempted non-exact materialization or legacy/producer fallback")
		}
		return next(argv)
	}})
}

func auditedSealedMaterializationWitness(e *entrypoint.Env, r npmCaptureRecord) error {
	marker, err := os.ReadFile(entrypoint.ProgramCaptureMarker(e.Home, "semver"))
	if err != nil || strings.TrimSpace(string(marker)) != r.Identity {
		return fmt.Errorf("production consumer marker does not name admitted identity")
	}
	lines, err := os.ReadFile(filepath.Join(e.Workspace, ".yolo/receipts.jsonl"))
	if err != nil {
		return err
	}
	found := false
	for _, line := range bytes.Split(bytes.TrimSpace(lines), []byte{'\n'}) {
		var receipt struct{ Kind, Bin, Declared, Resolved, Compatibility, Act string }
		if err := json.Unmarshal(line, &receipt); err != nil {
			return err
		}
		if receipt.Kind == entrypoint.ReceiptKindCapture && receipt.Bin == "semver" && receipt.Declared == "semver@7.7.2" && receipt.Act == entrypoint.ReceiptActMaterialize && receipt.Resolved == r.Entry && receipt.Compatibility == r.Identity {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("no production materialization receipt for admitted identity and entry")
	}
	return nil
}

func buildAuditedSealedCandidate(t *testing.T, ctx context.Context, root, owned string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("source location unavailable")
	}
	actual, err := filepath.EvalSymlinks(filepath.Join(root, "internal/cli/programcapturesealedintegration_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	caller, err := filepath.EvalSymlinks(file)
	if err != nil || caller != actual {
		t.Fatal("test/source checkout differs")
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = root
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatal("tracked source must be committed before owned candidate build", err)
		}
		return strings.TrimSpace(string(b))
	}
	head := git("rev-parse", "HEAD")
	git("diff", "--quiet", "HEAD", "--", ".")
	git("ls-files", "--error-unmatch", "internal/cli/programcapturesealedintegration_test.go")
	home := filepath.Join(owned, "matched-home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "just", "build-dev-bundle")
	cmd.Dir = root // build input only; runtime root comes from the emitted launcher's adjacency
	cmd.Env = envWithoutName(os.Environ(), "YOLO_REPO_ROOT")
	cmd.Env = append(envWithoutName(cmd.Env, "HOME"), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("shared matched-bundle build (within the existing <=5m proof bound): %v\n%s", err, out)
	}
	buildLines := strings.Split(strings.TrimSpace(string(out)), "\n")
	exe := strings.TrimSpace(buildLines[len(buildLines)-1])
	if !filepath.IsAbs(exe) {
		t.Fatalf("matched build did not print an absolute launcher path: %q", exe)
	}
	info, err := buildinfo.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	stampBytes, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "share", "yolo-jail", "yolo-build-stamp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stamp struct {
		SnapshotIdentity string `json:"snapshotIdentity"`
		BuildKind        string `json:"buildKind"`
	}
	if err := json.Unmarshal(stampBytes, &stamp); err != nil || stamp.SnapshotIdentity == "" || stamp.BuildKind != "development" {
		t.Fatalf("invalid matched candidate stamp: %+v (%v)", stamp, err)
	}
	if err := auditSealedBuildInfo(info, stamp.SnapshotIdentity); err != nil {
		t.Fatal(err)
	}
	git("diff", "--quiet", "HEAD", "--", ".")
	t.Logf("owned matched candidate source=%s commit=%s identity=%s path=%s", root, head, stamp.SnapshotIdentity, exe)
	return exe
}

func envWithoutName(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, _ := strings.Cut(item, "=")
		if key != name {
			out = append(out, item)
		}
	}
	return out
}

func auditSealedBuildInfo(info *debug.BuildInfo, snapshotIdentity string) error {
	settings := map[string]string{}
	for _, s := range info.Settings {
		settings[s.Key] = s.Value
	}
	ldflags := settings["-ldflags"]
	if info.Path != "github.com/mschulkind-oss/yolo-jail/cmd/yolo" || info.Main.Path != "github.com/mschulkind-oss/yolo-jail" ||
		!strings.Contains(ldflags, "github.com/mschulkind-oss/yolo-jail/internal/version.SnapshotIdentity="+snapshotIdentity) ||
		!strings.Contains(ldflags, "github.com/mschulkind-oss/yolo-jail/internal/version.BuildKind=development") ||
		snapshotIdentity == "" {
		return fmt.Errorf("owned CLI build does not match the matched-bundle snapshot identity")
	}
	return nil
}

func TestAuditedRegistryConsumerRefusesLegacyBeforeExecution(t *testing.T) {
	for _, wire := range []string{"", entrypoint.MarshalProgramCaptures(map[string]entrypoint.ProgramCaptureDelivery{"semver": {Declared: "semver@7.7.2", Compatibility: strings.Repeat("a", 64), Platform: capture.Platform()}})} {
		e := entrypoint.NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_WORKSPACE": t.TempDir(), entrypoint.CapturesDirEnv: t.TempDir(), entrypoint.ProgramCapturesEnv: wire})
		d := entrypoint.ProgramCaptureDelivery{Declared: "semver@7.7.2", Compatibility: strings.Repeat("a", 64), Platform: capture.Platform()}
		calls := 0
		err := auditedSealedRefresh(context.Background(), e, d, func(argv []string) error { calls++; return fmt.Errorf("safe pure materializer sentinel") })
		if err == nil {
			t.Fatal("consumer swallowed refusal")
		}
		want := 0
		if wire != "" {
			want = 1
		}
		if calls != want {
			t.Fatalf("consumer wiring/fallback calls=%d want=%d", calls, want)
		}
	}
}

func TestAuditedRegistryBuildIdentityRejectsUnrelatedCLI(t *testing.T) {
	identity := strings.Repeat("a", 64)
	good := debug.BuildInfo{Path: "github.com/mschulkind-oss/yolo-jail/cmd/yolo", Main: debug.Module{Path: "github.com/mschulkind-oss/yolo-jail"}, Settings: []debug.BuildSetting{{Key: "-ldflags", Value: "-X github.com/mschulkind-oss/yolo-jail/internal/version.SnapshotIdentity=" + identity + " -X github.com/mschulkind-oss/yolo-jail/internal/version.BuildKind=development"}}}
	if err := auditSealedBuildInfo(&good, identity); err != nil {
		t.Fatal(err)
	}
	if err := auditSealedBuildInfo(&good, strings.Repeat("b", 64)); err == nil {
		t.Fatal("accepted different matched snapshot identity")
	}
	good.Settings[0].Value = strings.Replace(good.Settings[0].Value, "BuildKind=development", "BuildKind=installed", 1)
	if err := auditSealedBuildInfo(&good, identity); err == nil {
		t.Fatal("accepted non-development launcher")
	}
	good.Settings[0].Value = "-X SnapshotIdentity=" + identity + " -X BuildKind=development"
	if err := auditSealedBuildInfo(&good, identity); err == nil {
		t.Fatal("accepted launcher without full version package identity")
	}
	good.Path = "unrelated"
	good.Settings[0].Value = "-X github.com/mschulkind-oss/yolo-jail/internal/version.SnapshotIdentity=" + identity + " -X github.com/mschulkind-oss/yolo-jail/internal/version.BuildKind=development"
	if err := auditSealedBuildInfo(&good, identity); err == nil {
		t.Fatal("accepted unrelated executable")
	}
}

func auditedSealedResolutionGate(raw []byte, next func() error) error {
	if err := auditSealedSemverResolution(raw); err != nil {
		return err
	}
	return next()
}

func TestAuditedRegistryRootLifecycleHooksStopBeforeChild(t *testing.T) {
	for _, hook := range []string{"preinstall", "install", "postinstall", "prepublish", "preprepare", "prepare", "postprepare", "prepack", "postpack"} {
		root := map[string]any{"dependencies": map[string]any{"semver": "7.7.2"}, "scripts": map[string]string{hook: "echo must-not-run"}}
		calls := 0
		err := auditedSealedResolutionGate(auditedSealedJSON(auditedSealedResolutionFixture(root)), func() error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatalf("%s reached child", hook)
		}
	}
	calls := 0
	err := auditedSealedResolutionGate(auditedSealedJSON(auditedSealedResolutionFixture(map[string]any{"dependencies": map[string]any{"semver": "7.7.2"}})), func() error { calls++; return nil })
	if err != nil || calls != 1 {
		t.Fatal("pure audited baseline was rejected", err)
	}
}
