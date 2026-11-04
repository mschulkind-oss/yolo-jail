package run

// migrateofferceiling_test.go pins the launch's offer when converting a legacy workspace writes
// a ceiling line into a TOML user config (docs/reference/loophole-grants.md LG-D16): the file and
// the lines it gains are named before the offer's question, and the lines do not survive a
// refusal at either prompt — a no at the offer writes nothing, and a no at the config-change gate
// that follows takes them back, naming the user's own command that admits the request.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ceilingUserTOML = "# mine\npacks = [\"audio\"]\n"

// runCeilingOffer runs a launch whose user config is ceilingUserTOML and whose workspace is a
// legacy file switching audio on, answering answers.
func runCeilingOffer(t *testing.T, answers string) (*offerLaunch, string) {
	t.Helper()
	var userPath string
	l := runOfferLaunchWith(t, "{}\n", true, "", func(o *Options) {
		// One line per read, as a terminal gives it: each prompt's scanner reads its own answer.
		o.Stdin = &lineReader{lines: strings.SplitAfter(answers, "\n")}
		userPath = filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.toml")
		if err := os.Remove(filepath.Join(filepath.Dir(userPath), "config.jsonc")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(userPath, []byte(ceilingUserTOML), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(o.Workspace, "yolo-jail.jsonc"),
			[]byte(`{"loopholes": {"audio": {"enabled": true}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	return l, userPath
}

// lineReader returns one line per Read, as a terminal in canonical mode does.
type lineReader struct{ lines []string }

func (r *lineReader) Read(p []byte) (int, error) {
	for len(r.lines) > 0 && r.lines[0] == "" {
		r.lines = r.lines[1:]
	}
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.lines[0])
	r.lines[0] = r.lines[0][n:]
	return n, nil
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTheOfferNamesTheUserCeilingLineBeforeTheQuestion(t *testing.T) {
	l, userPath := runCeilingOffer(t, "n\n")
	s := l.stderr.String()
	q := strings.Index(s, "Convert to TOML now?")
	named := strings.Index(s, userPath)
	line := strings.Index(s, "+ allow.audio.on_request: use")
	if q < 0 || named < 0 || line < 0 || named > q || line > q {
		t.Fatalf("the offer does not name %s and the line it gains before the question:\n%s", userPath, s)
	}
	if got := readFile(t, userPath); got != ceilingUserTOML {
		t.Errorf("a no at the offer changed the user config:\n%s", got)
	}
}

func TestANoAtTheGateTakesBackTheUserCeilingLine(t *testing.T) {
	l, userPath := runCeilingOffer(t, "y\nn\n")
	s := l.stderr.String()
	if !strings.Contains(s, "Converted.") {
		t.Fatalf("the offer's y did not convert:\n%s", s)
	}
	if l.rc == 0 || l.launched {
		t.Fatalf("the gate's no did not stop the launch (rc %d, launched %v):\n%s", l.rc, l.launched, s)
	}
	if got := readFile(t, userPath); got != ceilingUserTOML {
		t.Errorf("the ceiling line survived the gate's no; the user config is:\n%s", got)
	}
	if !strings.Contains(s, "yolo allow audio on_request use") {
		t.Errorf("the take-back does not name the command that admits the request:\n%s", s)
	}
}

func TestAYesAtTheGateKeepsTheUserCeilingLine(t *testing.T) {
	l, userPath := runCeilingOffer(t, "y\ny\n")
	l.mustLaunch(t)
	if got := readFile(t, userPath); !strings.Contains(got, "[allow.audio]\non_request = [\"use\"]") {
		t.Errorf("the approved launch lost the ceiling line; the user config is:\n%s", got)
	}
}
