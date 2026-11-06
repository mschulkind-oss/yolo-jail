package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestTheJailsYoloCarriesItsMatchedBundleStamp pins the identity of the launcher
// TestMain built, not git metadata from the module build-input directory.
func TestTheJailsYoloCarriesItsMatchedBundleStamp(t *testing.T) {
	requireJail(t)
	data, err := os.ReadFile(filepath.Join(bundleRoot, "yolo-build-stamp.json"))
	if err != nil {
		t.Fatalf("read matched bundle stamp: %v", err)
	}
	var stamp struct {
		Commit           string `json:"commit"`
		SnapshotIdentity string `json:"snapshotIdentity"`
		BuildKind        string `json:"buildKind"`
	}
	if err := json.Unmarshal(data, &stamp); err != nil {
		t.Fatalf("decode matched bundle stamp: %v", err)
	}
	if stamp.Commit == "" || stamp.SnapshotIdentity == "" || stamp.BuildKind != "development" {
		t.Fatalf("TestMain did not select a stamped development bundle: %+v", stamp)
	}
	wantCommit := stamp.Commit
	if len(wantCommit) > 12 {
		wantCommit = wantCommit[:12]
	}

	dir := writeProject(t, "{}")
	awaitDetachedWriters(t, dir, launchHome(launchEnvironment(runConfig{})))
	args := append(append(jailRunArgs(), "--"), "yolo", "--version", "--full")
	r := runCommand(t, dir, args, withHostSemantics())
	if r.rc != 0 {
		t.Fatalf("yolo --version --full in the jail: rc=%d\n%s", r.rc, r.combined())
	}
	hostCmd := exec.Command(yoloBin, "--version", "--full")
	hostCmd.Env = envWithout("YOLO_VERSION")
	hostVersionOutput, err := hostCmd.Output()
	if err != nil {
		t.Fatalf("matched host yolo --version --full: %v", err)
	}
	hostOutput := string(hostVersionOutput)
	hostVersion := versionToken(t, hostOutput)
	guestVersion := versionToken(t, r.stdout)
	if guestVersion != hostVersion {
		t.Errorf("the jail's matched yolo version = %q, want host bundle version %q", guestVersion, hostVersion)
	}
	if !versionEncodesCommit(hostVersion, wantCommit) {
		t.Errorf("matched version %q does not encode stamped commit %q in its build metadata", hostVersion, wantCommit)
	}
	hostHash := versionField(t, hostOutput, "binary sha256: ")
	wantHostHash, err := binaryFileHash(yoloBin)
	if err != nil {
		t.Errorf("hash matched host yolo binary %s: %v", yoloBin, err)
	} else if hostHash != wantHostHash {
		t.Errorf("matched host binary sha256 %q does not match host executable %s sha256 %q", hostHash, yoloBin, wantHostHash)
	}
	guestHash := versionField(t, r.stdout, "binary sha256: ")
	if err := matchedGuestBinaryHashError(guestHash, bundleRoot, goruntime.GOARCH); err != nil {
		t.Error(err)
	}
	for _, output := range []struct {
		name string
		text string
	}{{"host", hostOutput}, {"guest", r.stdout}} {
		for _, want := range []string{"\ncommit: " + wantCommit + "\n",
			"\nsnapshot identity: " + stamp.SnapshotIdentity + "\n", "\nbuild kind: development\n"} {
			if !strings.Contains(output.text, want) {
				t.Errorf("the %s yolo does not carry matched bundle field %q:\n%s", output.name, strings.Trim(want, "\n"), output.text)
			}
		}
	}
}

func TestMatchedBundleStampTestUsesPairedLinuxHashAtCallsite(t *testing.T) {
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("cannot locate versionstamp_test.go to verify the matched-stamp callsite")
	}
	file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatalf("parse versionstamp test source: %v", err)
	}
	var matchedTest *ast.FuncDecl
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok && function.Name.Name == "TestTheJailsYoloCarriesItsMatchedBundleStamp" {
			matchedTest = function
			break
		}
	}
	if matchedTest == nil {
		t.Fatal("matched-stamp integration test is missing")
	}
	found := false
	ast.Inspect(matchedTest.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 3 {
			return true
		}
		function, ok := call.Fun.(*ast.Ident)
		if !ok || function.Name != "matchedGuestBinaryHashError" {
			return true
		}
		guestHash, guestOK := call.Args[0].(*ast.Ident)
		bundleRoot, bundleOK := call.Args[1].(*ast.Ident)
		arch, archOK := call.Args[2].(*ast.SelectorExpr)
		if !guestOK || guestHash.Name != "guestHash" || !bundleOK || bundleRoot.Name != "bundleRoot" || !archOK {
			return true
		}
		archPackage, packageOK := arch.X.(*ast.Ident)
		found = packageOK && archPackage.Name == "goruntime" && arch.Sel.Name == "GOARCH"
		return true
	})
	if !found {
		t.Fatal("matched-stamp integration test must compare the guest hash with bundleRoot/bin/linux-<runtime.GOARCH>/yolo")
	}
}

func TestMatchedGuestBinaryHashUsesPairedLinuxHelper(t *testing.T) {
	root := t.TempDir()
	hostPath := filepath.Join(root, "bin", "yolo")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hostPath, []byte("native host executable A"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostHash, err := binaryFileHash(hostPath)
	if err != nil {
		t.Fatal(err)
	}
	wrongGuestHash := fmt.Sprintf("%x", sha256.Sum256([]byte("wrong guest executable C")))

	for arch, helperBytes := range map[string]string{
		"amd64": "paired Linux helper B",
		"arm64": "paired Linux helper B2",
	} {
		t.Run(arch, func(t *testing.T) {
			pairedPath := filepath.Join(root, "bin", "linux-"+arch, "yolo")
			if err := os.MkdirAll(filepath.Dir(pairedPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(pairedPath, []byte(helperBytes), 0o755); err != nil {
				t.Fatal(err)
			}
			pairedHash, err := binaryFileHash(pairedPath)
			if err != nil {
				t.Fatal(err)
			}
			if pairedHash == hostHash || pairedHash == wrongGuestHash || hostHash == wrongGuestHash {
				t.Fatalf("fixture hashes must be distinct: host=%s paired=%s wrong guest=%s", hostHash, pairedHash, wrongGuestHash)
			}
			if err := matchedGuestBinaryHashError(pairedHash, root, arch); err != nil {
				t.Fatalf("paired helper hash rejected: %v", err)
			}
			if err := matchedGuestBinaryHashError(wrongGuestHash, root, arch); err == nil || !strings.Contains(err.Error(), pairedPath) {
				t.Fatalf("wrong guest hash error = %v, want rejection naming paired helper %s", err, pairedPath)
			}
		})
	}
}

func matchedGuestBinaryHashError(guestHash, bundleRoot, arch string) error {
	pairedYolo := filepath.Join(bundleRoot, "bin", "linux-"+arch, "yolo")
	wantHash, err := binaryFileHash(pairedYolo)
	if err != nil {
		return fmt.Errorf("hash paired Linux yolo %s: %w", pairedYolo, err)
	}
	if guestHash != wantHash {
		return fmt.Errorf("guest binary sha256 %q does not match paired Linux yolo %s sha256 %q", guestHash, pairedYolo, wantHash)
	}
	return nil
}

func binaryFileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func versionField(t *testing.T, output, label string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	t.Fatalf("version output has no %q field:\n%s", label, output)
	return ""
}

func versionToken(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "yolo-jail" {
			return fields[1]
		}
	}
	t.Fatalf("version output has no yolo-jail version line:\n%s", output)
	return ""
}

// versionEncodesCommit reports whether version's build metadata (after "+") names commit as one
// of its dot-separated fields, "g<commit>". version.Normalize writes it in every stamped form:
// <tag>+g<hash> on the tag itself, <tag>+<n>.g<hash> after it, either with ".dirty" appended.
func versionEncodesCommit(version, commit string) bool {
	_, meta, ok := strings.Cut(version, "+")
	if !ok || commit == "" {
		return false
	}
	for _, field := range strings.Split(meta, ".") {
		if field == "g"+commit {
			return true
		}
	}
	return false
}

// TestVersionEncodesCommitAcceptsTheOnTagForm pins versionEncodesCommit against
// version.Normalize's three stamped forms. The on-tag form is the one the
// 2026-11-04 macOS nightly met: it ran on the v0.16.1 commit, the matched version
// was 0.16.1+g3ccc28def766, and a suffix check for ".g<hash>" refused it.
func TestVersionEncodesCommitAcceptsTheOnTagForm(t *testing.T) {
	for _, tc := range []struct {
		version, commit string
		want            bool
	}{
		{"0.16.1+g3ccc28def766", "3ccc28def766", true},
		{"0.16.1+55.g163fd8fb34ff", "163fd8fb34ff", true},
		{"0.16.1+55.g163fd8fb34ff.dirty", "163fd8fb34ff", true},
		{"0.16.1", "3ccc28def766", false},
		{"0.16.1+g3ccc28def766", "163fd8fb34ff", false},
		{"0.16.1+55.g163fd8fb34ff", "163fd8fb", false},
		{"0.16.1+gx3ccc28def766", "3ccc28def766", false},
	} {
		if got := versionEncodesCommit(tc.version, tc.commit); got != tc.want {
			t.Errorf("versionEncodesCommit(%q, %q) = %v, want %v", tc.version, tc.commit, got, tc.want)
		}
	}
}
