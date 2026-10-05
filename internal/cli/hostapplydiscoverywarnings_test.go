package cli

// hostapplydiscoverywarnings_test.go pins where `yolo host apply` says the loophole loader's own
// warnings since the notch line reads loophole discovery (run.HostDoorwayLoopholes and
// run.HostInlineLoopholes): on the process's stderr, through the loopholes package's sink, once
// per process however many times the apply discovers, and above the notch line whose loophole
// outcome they explain. hostdoorwaysets.go's header says why they are not the report's lines.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// streamWriter writes into one file shared with os.Stderr, each write opened by tag, so a test
// can read the report's lines and the process's stderr lines in the order they were written.
type streamWriter struct {
	f   *os.File
	tag string
}

func (w streamWriter) Write(p []byte) (int, error) {
	if _, err := w.f.Write(append([]byte(w.tag), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// A pack whose loophole manifest carries a key this build does not know, beside a `loopholes`
// block, so both of the apply's discoveries read it: the loader warns about the key, and the
// apply must neither swallow the warning nor say it once per discovery.
func TestHostApplySaysLoopholeDiscoveryWarningsOnStderrAboveTheNotchLine(t *testing.T) {
	home := t.TempDir()
	pack := filepath.Join(t.TempDir(), "skew")
	writeFile(t, filepath.Join(pack, "pack.json"),
		`{"name":"skew","contributes":[{"kind":"loophole","from":"loopholes/skewhole"}]}`)
	writeFile(t, filepath.Join(pack, "loopholes", "skewhole", "manifest.jsonc"), `{
		"name": "skewhole", "description": "d", "version": 1, "default_enabled": true,
		"future_key": 1, "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "skew", "--socket", "{socket}"],
		                "publishes": "socket", "scope": "host"}}`)
	entry := `{"source":"file://` + pack + `","name":"skew"}`
	selectPacks(t, home, entry)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[`+entry+`],"loopholes":{"skewhole":{"enabled":true}}}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	defaultReport(t)

	f, err := os.Create(filepath.Join(t.TempDir(), "stream"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	const reportTag = "report|"
	saved := os.Stderr
	os.Stderr = f
	rc := applyHostSurveyed(streamWriter{f, reportTag}, streamWriter{f, reportTag}, false, false, nil,
		&hostApplySurvey{})
	os.Stderr = saved
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	stream := string(raw)
	if rc != 0 {
		t.Fatalf("observe apply rc=%d\n%s", rc, stream)
	}

	header, warning, notch, warnings := -1, -1, -1, 0
	for i, l := range strings.Split(stream, "\n") {
		switch {
		case strings.HasPrefix(l, reportTag+"host apply"):
			header = i
		case strings.Contains(l, `ignoring unknown key "future_key"`):
			warnings++
			warning = i
			if !strings.HasPrefix(l, "warning: loophole skewhole: ") {
				t.Errorf("the loader's warning is not the process's own stderr line: %q", l)
			}
		case strings.HasPrefix(l, reportTag) && strings.Contains(l, doesNotApplyClause):
			notch = i
		}
	}
	if warnings != 1 {
		t.Fatalf("the loader's warning is said %d times, want once (the apply discovers twice, and "+
			"the loader says each line once per process):\n%s", warnings, stream)
	}
	if header < 0 || notch < 0 {
		t.Fatalf("fixture bug: no header or no notch line in the report:\n%s", stream)
	}
	if !(header < warning && warning < notch) {
		t.Errorf("the loader's warning is not said between the header and the notch line whose "+
			"loophole outcome it explains (header %d, warning %d, notch %d):\n%s",
			header, warning, notch, stream)
	}
}
