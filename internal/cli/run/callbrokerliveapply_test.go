package run

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestConfigApplyReplacesSelectedCallBrokerRootsFromTheApprovedGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := filepath.Join(home, "fixture-workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	sf := brokerscope.File{Source: "fixture-tasks", LaunchID: id, PID: os.Getpid(), Container: "fixture-container", Workspace: workspace,
		Form: brokerscope.FormCallRoot, Repos: []string{"team:alpha"}, LiveScope: true}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	before, err := brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	delta := scopeDelta{label: "tasks", source: sf.Source, desired: []string{"team:beta"}, files: []liveScopeFile{{Path: path, File: before, LiveScope: true}}}
	added, removed := delta.addedRemoved()
	if !reflect.DeepEqual(added, []string{"team:beta"}) || !reflect.DeepEqual(removed, []string{"team:alpha"}) {
		t.Fatalf("root change not noticed: +%v -%v", added, removed)
	}
	o := &Options{Workspace: workspace}
	o.recordApprovedScopes(map[string][]config.ScopeRepo{"tasks": {{Repo: "team:beta"}}}, nil, nil)
	change := applyChange{kind: changeScope, scope: delta}
	if err := o.applyStep(&applyRun{}, &change, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Form != brokerscope.FormCallRoot || !reflect.DeepEqual(after.Repos, []string{"team:beta"}) || after.LaunchID != id {
		t.Fatalf("live call-broker scope did not take the approved roots: %+v", after)
	}
}
