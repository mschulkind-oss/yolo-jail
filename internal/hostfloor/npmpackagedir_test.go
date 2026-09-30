package hostfloor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestNpmPackageDirIsWhereTheInstallPutThePackage pins Record.NpmPackageDir to the layout the npm
// install writes: after a real Ensure through the fake npm, the directory it names holds the
// installed package's package.json. `yolo check` reads a program's declared model catalog there
// (MM-D19), so a prefix moved in installNpm alone fails here rather than leaving the check
// looking in an empty directory. A record npm did not install names none.
func TestNpmPackageDirIsWhereTheInstallPutThePackage(t *testing.T) {
	w := newWorld(t)
	w.publish("@scope/agent", "1.2.3", "bin=agent")
	p := npmProgram("agent", "agent", "@scope/agent")
	st, _, err := w.floor.Ensure(context.Background(), p)
	if err != nil {
		t.Fatalf("Ensure: %v\n%s", err, w.out.String())
	}
	dir := st.Record.NpmPackageDir("@scope/agent@^1")
	if dir == "" {
		t.Fatal("an npm record names no package directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Errorf("NpmPackageDir = %s, which holds no package.json: %v", dir, err)
	}
	if got := (&Record{Via: "installer", Dir: st.Record.Dir}).NpmPackageDir("@scope/agent"); got != "" {
		t.Errorf("an installer record names %q, want none", got)
	}
	var none *Record
	if got := none.NpmPackageDir("@scope/agent"); got != "" {
		t.Errorf("a nil record names %q, want none", got)
	}
}
