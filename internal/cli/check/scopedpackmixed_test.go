package check

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

type scopedWholeStageObserver struct {
	bytes.Buffer
	t      *testing.T
	root   string
	staged map[string]string
}

func (w *scopedWholeStageObserver) Write(data []byte) (int, error) {
	snapshot := scopedCheckSnapshot(w.t, w.root)
	if len(snapshot) > 1 {
		w.staged = map[string]string{}
		for path, value := range snapshot {
			w.staged[strings.TrimPrefix(path, w.root)] = value
		}
	}
	return w.Buffer.Write(data)
}

func TestSectionPacksMixedScopesAddNoEffectsToWholeReadinessBaseline(t *testing.T) {
	user := testconfig.User(t, "packs=[]\n")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	corpus := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
	for _, name := range []string{"one", "two", "whole"} {
		path := filepath.Join(corpus, name, "skills", name+"-chosen/SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	trace := filepath.Join(t.TempDir(), "transport-denied")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+shquote.Quote(trace)+"\nexit 91\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	whole := "{pack='local/corpus',subdir='whole'}"
	run := func(body string) (map[string]string, string) {
		t.Helper()
		if err := os.WriteFile(user.Path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		before := scopedCheckSnapshot(t, corpus, filepath.Dir(user.Path), paths.PacksDir())
		parent := t.TempDir()
		root := filepath.Join(parent, "stage")
		factoryCalls := 0
		writer := &scopedWholeStageObserver{t: t, root: root}
		r := newReporter(writer, false)
		o := &Options{packTreeFactory: func() (string, error) { factoryCalls++; err := os.MkdirAll(root, 0755); return root, err }}
		o.sectionPacks(r, jsonx.NewOrderedMap())
		if r.failed != 0 {
			t.Fatalf("mixed selection: %s", writer.String())
		}
		if factoryCalls != 1 {
			t.Fatalf("scoped selection added stage roots: %d", factoryCalls)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Fatalf("whole readiness tree not cleaned: %v", err)
		}
		if after := scopedCheckSnapshot(t, corpus, filepath.Dir(user.Path), paths.PacksDir()); !reflect.DeepEqual(before, after) {
			t.Fatal("scoped selection wrote source/config/store")
		}
		for _, p := range o.selectedPacks {
			if p.Projection != nil && p.SourceRoot != "" {
				t.Fatalf("scoped carrier was copied: %+v", p)
			}
		}
		if len(writer.staged) == 0 {
			t.Fatal("whole readiness baseline never staged")
		}
		return writer.staged, writer.String()
	}
	baseline, _ := run("packs=[" + whole + "]\n")
	mixed, out := run("packs=[{pack='local/corpus',subdir='one',skills={dir='skills'}},{pack='local/corpus',subdir='two',skills={dir='skills'}}," + whole + "]\n")
	if !reflect.DeepEqual(baseline, mixed) {
		t.Fatalf("scoped entries added/differed readiness staging: baseline=%v mixed=%v", baseline, mixed)
	}
	previous := -1
	for _, header := range []string{"packs[0] local/corpus", "packs[1] local/corpus", "packs[2] local/corpus"} {
		at := strings.Index(out, header)
		if at <= previous {
			t.Fatalf("mixed tuple lost/out of order %q: %s", header, out)
		}
		previous = at
	}
	for _, want := range []string{`subdir="one"`, `subdir="two"`, `subdir="whole"`, `mode=raw`, `mode=whole`, `selected: [one-chosen]`, `selected: [two-chosen]`} {
		if !strings.Contains(out, want) {
			t.Fatalf("mixed group missing %q: %s", want, out)
		}
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("mixed check attempted Git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.PacksDir(), "payloads")); !os.IsNotExist(err) {
		t.Fatalf("mixed check published payload: %v", err)
	}
	if _, err := os.Stat(packsrc.LockPath(user.Path)); !os.IsNotExist(err) {
		t.Fatalf("mixed check wrote pin: %v", err)
	}
	// Keep identity validation in the real producer: the three roots are legal,
	// not an artificial same-tuple duplication admitted only for this test.
	_, problems, err := config.LoadPackEntries()
	if err != nil || len(problems) > 0 {
		t.Fatalf("mixed producer invalid: %v %v", err, problems)
	}
}
