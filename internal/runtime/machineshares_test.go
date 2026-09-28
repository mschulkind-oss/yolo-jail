package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures below are the JSON shapes podman prints, transcribed from its source
// (cmd/podman/machine/{list,inspect}.go, pkg/machine/vmconfigs/config.go and
// common/pkg/config's Connection, podman v4.9.0 through main). The machine config is the
// file `podman machine init` writes under ConfigDir; its Mounts are the `-v` list.

// machineConfigJSON is a podman 5/6 machine config with the guide's four shares.
const machineConfigJSON = `{
  "ConfigPath": {"Path": "/ignored"},
  "Mounts": [
    {"ReadOnly": false, "Source": "/Users", "Tag": "a2a0ee2c717462feb1de2f5afd59de5fd2d8", "Target": "/Users", "Type": "virtiofs", "OriginalInput": "/Users:/Users"},
    {"ReadOnly": false, "Source": "/private", "Tag": "71708eb255bc230cd7c91dd26f7667a7b938", "Target": "/private", "Type": "virtiofs"},
    {"ReadOnly": false, "Source": "/var/folders", "Tag": "a0bb3a2c8b0b02ba5958b0576f0d6530e104", "Target": "/var/folders", "Type": "virtiofs"},
    {"ReadOnly": false, "Source": "/opt/homebrew/Cellar", "Tag": "x", "Target": "/opt/homebrew/Cellar", "Type": "virtiofs"}
  ],
  "Name": "podman-machine-default",
  "Version": 1
}`

// fakeMachine answers the three podman probes ReadMachineShares makes, with its config
// file written under a temp dir, and records what was asked.
type fakeMachine struct {
	list, conn, inspect string
	asked               []string
}

func (f *fakeMachine) run(argv []string) (string, bool) {
	cmd := strings.Join(argv, " ")
	f.asked = append(f.asked, cmd)
	switch {
	case cmd == "podman machine list --format json":
		return f.list, f.list != ""
	case cmd == "podman system connection list --format json":
		return f.conn, f.conn != ""
	case strings.HasPrefix(cmd, "podman machine inspect "):
		return f.inspect, f.inspect != ""
	}
	return "", false
}

func writeMachineConfig(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func inspectJSON(dir, name string) string {
	return `[{"ConfigDir": {"Path": "` + dir + `"}, "ConnectionInfo": {}, "Name": "` + name +
		`", "Resources": {"CPUs": 4, "Memory": 8192}, "State": "running"}]`
}

func noEnv(string) string { return "" }

func TestReadMachineSharesReadsTheConfigFileInspectNames(t *testing.T) {
	dir := writeMachineConfig(t, "podman-machine-default", machineConfigJSON)
	f := &fakeMachine{
		list:    `[{"Name": "podman-machine-default", "Default": true, "Running": true, "VMType": "applehv"}]`,
		inspect: inspectJSON(dir, "podman-machine-default"),
	}
	got, ok := ReadMachineShares(f.run, noEnv)
	if !ok {
		t.Fatalf("a readable machine config was reported unknown; asked %v", f.asked)
	}
	if got.Machine != "podman-machine-default" || len(got.Shares) != 4 {
		t.Fatalf("got %+v", got)
	}
	if !got.Reaches("/opt/homebrew/Cellar/yolo-jail/0.12.0/share/yolo-jail") {
		t.Error("a Cellar the machine shares was reported unreachable — the guide's own setup refused")
	}
	if want := "podman machine inspect podman-machine-default"; !contains(f.asked, want) {
		t.Errorf("did not inspect the active machine by name; asked %v", f.asked)
	}
}

func TestReadMachineSharesRootfulMachineFoundThroughItsConnection(t *testing.T) {
	dir := writeMachineConfig(t, "work", machineConfigJSON)
	f := &fakeMachine{
		// `machine list` marks no machine Default when the default connection is the
		// rootful "<name>-root" one.
		list: `[{"Name": "podman-machine-default", "Default": false}, {"Name": "work", "Default": false}]`,
		conn: `[{"Name": "podman-machine-default", "URI": "ssh://a", "Default": false, "ReadWrite": true},
		        {"Name": "work-root", "URI": "ssh://b", "Default": true, "ReadWrite": true}]`,
		inspect: inspectJSON(dir, "work"),
	}
	got, ok := ReadMachineShares(f.run, noEnv)
	if !ok || got.Machine != "work" {
		t.Fatalf("got %+v ok=%v; asked %v", got, ok, f.asked)
	}
}

func TestReadMachineSharesUnknownIsNeverAGuess(t *testing.T) {
	dir := writeMachineConfig(t, "podman-machine-default", machineConfigJSON)
	good := inspectJSON(dir, "podman-machine-default")
	defList := `[{"Name": "podman-machine-default", "Default": true}]`
	cases := []struct {
		name string
		f    fakeMachine
		env  map[string]string
	}{
		{"list fails", fakeMachine{inspect: good}, nil},
		{"list is not JSON", fakeMachine{list: "NAME  VM TYPE", inspect: good}, nil},
		{"no machines", fakeMachine{list: "[]", inspect: good}, nil},
		{"two defaults", fakeMachine{list: `[{"Name":"a","Default":true},{"Name":"b","Default":true}]`, inspect: good}, nil},
		{"no default and the connection is not a machine", fakeMachine{
			list: `[{"Name": "podman-machine-default", "Default": false}]`,
			conn: `[{"Name": "remote", "Default": true}]`, inspect: good}, nil},
		{"inspect fails", fakeMachine{list: defList}, nil},
		{"inspect has neither ConfigDir nor ConfigPath", fakeMachine{list: defList,
			inspect: `[{"Name": "podman-machine-default"}]`}, nil},
		{"config file missing", fakeMachine{list: defList,
			inspect: inspectJSON(t.TempDir(), "podman-machine-default")}, nil},
		{"CONTAINER_HOST points podman elsewhere", fakeMachine{list: defList, inspect: good},
			map[string]string{"CONTAINER_HOST": "ssh://elsewhere"}},
		{"CONTAINER_CONNECTION picks another connection", fakeMachine{list: defList, inspect: good},
			map[string]string{"CONTAINER_CONNECTION": "other"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.f
			if _, ok := ReadMachineShares(f.run, func(k string) string { return tc.env[k] }); ok {
				t.Fatal("an unreadable or ambiguous machine was reported known — a refusal would rest on a guess")
			}
		})
	}
}

func TestParseMachineConfigSharesUnknownShapes(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"Mounts": null}`, `{"Mounts": []}`, `not json`,
		`{"Mounts": [{"Source": "relative", "Target": "/x"}]}`,
	} {
		if _, ok := ParseMachineConfigShares([]byte(raw)); ok {
			t.Errorf("%s was read as a share list", raw)
		}
	}
	// podman's own `-v <path>` with no target mounts the path at itself.
	got, ok := ParseMachineConfigShares([]byte(`{"Mounts": [{"Source": "/Volumes/Work"}]}`))
	if !ok || len(got) != 1 || got[0].Target != "/Volumes/Work" {
		t.Errorf("an empty Target did not default to Source: %+v ok=%v", got, ok)
	}
}

func TestParseMachineInspectConfigFilePodman4(t *testing.T) {
	got, ok := ParseMachineInspectConfigFile(`[{"ConfigPath": {"Path": "/Users/me/.config/containers/podman/machine/qemu/m.json"}, "Name": "m"}]`)
	if !ok || got != "/Users/me/.config/containers/podman/machine/qemu/m.json" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestReachesIsBySegmentOnTheTarget(t *testing.T) {
	s := MachineShares{Shares: []MachineShare{
		{Source: "/Users", Target: "/Users"},
		{Source: "/Volumes/Ext", Target: "/mnt/ext"},
	}}
	cases := map[string]bool{
		"/Users":                        true,
		"/Users/me/proj":                true,
		"/users/me/proj":                true, // a case-insensitive Mac volume
		"/Users-other/proj":             false,
		"/opt/homebrew/Cellar/yolo":     false,
		"/Volumes/Ext/proj":             false, // the VM has it at /mnt/ext, not here
		"/mnt/ext/proj":                 true,
		"relative/path":                 false,
		"/Users/me/../../opt/elsewhere": false,
	}
	for p, want := range cases {
		if got := s.Reaches(p); got != want {
			t.Errorf("Reaches(%q) = %v, want %v", p, got, want)
		}
	}
	if !(MachineShares{Shares: []MachineShare{{Source: "/", Target: "/"}}}).Reaches("/anything") {
		t.Error("a share of / reaches everything")
	}
}

func TestUnreachableTakesEitherSpelling(t *testing.T) {
	s := MachineShares{Shares: []MachineShare{{Source: "/var/folders", Target: "/var/folders"}}}
	resolve := func(p string) string {
		if strings.HasPrefix(p, "/tmp/") {
			return "/var/folders/xy/" + strings.TrimPrefix(p, "/tmp/")
		}
		return p
	}
	got := s.Unreachable([]string{"/var/folders/a", "/tmp/b", "/opt/c", "/opt/c", "named-volume",
		"/dev/null"}, resolve)
	if strings.Join(got, ",") != "/opt/c" {
		t.Errorf("got %v, want only /opt/c (the VM has its own /dev/null)", got)
	}
}

// A path that does not exist yet resolves through its nearest existing ancestor, so a
// socket dir under a symlinked /tmp is judged by where it will really be.
func TestResolveThroughExisting(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	realResolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ResolveThroughExisting(filepath.Join(link, "not", "yet")), filepath.Join(realResolved, "not", "yet"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got := ResolveThroughExisting("relative"); got != "relative" {
		t.Errorf("a relative path changed: %s", got)
	}
}

func TestRefusalNamesThePathAndTheGuideShapedFix(t *testing.T) {
	dir := writeMachineConfig(t, "podman-machine-default", strings.Replace(machineConfigJSON,
		`,
    {"ReadOnly": false, "Source": "/opt/homebrew/Cellar", "Tag": "x", "Target": "/opt/homebrew/Cellar", "Type": "virtiofs"}`, "", 1))
	f := &fakeMachine{
		list:    `[{"Name": "podman-machine-default", "Default": true}]`,
		inspect: inspectJSON(dir, "podman-machine-default"),
	}
	s, ok := ReadMachineShares(f.run, noEnv)
	if !ok {
		t.Fatal("fixture unreadable")
	}
	src := "/opt/homebrew/Cellar/yolo-jail/0.12.0/share/yolo-jail"
	un := s.Unreachable([]string{src, "/Users/me/proj"}, nil)
	if len(un) != 1 || un[0] != src {
		t.Fatalf("unreachable = %v", un)
	}
	msg := s.UnsharedRefusal(un)
	for _, want := range []string{
		src,
		"statfs",
		"podman machine rm",
		// The guide's init command: the defaults repeated, then Cellar — never the one keg.
		"podman machine init -v /Users:/Users -v /private:/private -v /var/folders:/var/folders -v /opt/homebrew/Cellar:/opt/homebrew/Cellar",
		"podman machine start",
		"userguide/guides/macos.md",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q:\n%s", want, msg)
		}
	}
}

func TestMachineInitCommandNamesANonDefaultMachine(t *testing.T) {
	s := MachineShares{Machine: "work", Shares: []MachineShare{{Source: "/Users", Target: "/Users", ReadOnly: true}}}
	got := s.MachineInitCommand([]string{"/Volumes/My Disk/proj"})
	want := "podman machine init work -v /Users:/Users:ro -v '/Volumes/My Disk/proj:/Volumes/My Disk/proj'"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// The prefix's bin/ lies inside its bundle, and two kegs share one Cellar: the command
// adds each outer folder once.
func TestMachineInitCommandAddsEachFolderOnce(t *testing.T) {
	s := MachineShares{Shares: []MachineShare{{Source: "/Users", Target: "/Users"}}}
	got := s.MachineInitCommand([]string{
		"/opt/homebrew/Cellar/yolo-jail/1/share/yolo-jail/bin/linux-arm64",
		"/opt/homebrew/Cellar/yolo-jail/1/share/yolo-jail",
		"/Volumes/Work/proj/sub",
		"/Volumes/Work/proj",
	})
	want := "podman machine init -v /Users:/Users -v /Volumes/Work/proj:/Volumes/Work/proj " +
		"-v /opt/homebrew/Cellar:/opt/homebrew/Cellar"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
