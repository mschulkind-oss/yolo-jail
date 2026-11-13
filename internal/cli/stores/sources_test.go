package stores

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestEachTechnologysSectionsAppearOncePerListedSource (named-setups.md §12.5, slice 11f): with
// Podman and Apple Container both listed, the image store and the scratch volumes are collected
// once per technology, each row tagged with its confinement, headed with the technology's name,
// and reclaimed by that technology's prune. The machine-wide rows appear once, untagged.
func TestEachTechnologysSectionsAppearOncePerListedSource(t *testing.T) {
	o, _ := testOptions(t)
	o.Sources = []Source{podmanListed(), appleContainerListed()}
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		if strings.Join(argv, " ") == "podman images -a --format json" {
			return prune.ProbeResult{Ran: true, Stdout: podmanListing}
		}
		return prune.ProbeResult{Ran: false}
	}
	out := new(strings.Builder)
	o.Out = out
	o.NoRecord = true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	rep := Inventory(o)

	podman := storeByKey(t, rep, "images.yolo")
	ac := storeByKey(t, rep, "images.yolo@apple-container")
	if podman.Confinement != "podman" || ac.Confinement != "apple-container" {
		t.Errorf("image rows' confinements: %q, %q", podman.Confinement, ac.Confinement)
	}
	if !strings.Contains(podman.Reclaimer.Trigger, "`YOLO_SETUP=podman-self yolo prune --apply`") {
		t.Errorf("Podman's image row names trigger %q, want Podman's own prune", podman.Reclaimer.Trigger)
	}
	if !strings.Contains(ac.Reclaimer.Trigger, "`YOLO_SETUP=apple-container-self yolo prune --apply`") {
		t.Errorf("Apple Container's image row names trigger %q, want its own prune", ac.Reclaimer.Trigger)
	}
	for _, key := range []string{"volumes.scratch.gone", "volumes.scratch.gone@apple-container"} {
		if s := storeByKey(t, rep, key); s.Confinement == "" {
			t.Errorf("%s carries no confinement", key)
		}
	}
	for _, s := range rep.Stores {
		if s.Section == SectionState || s.Section == SectionCache || s.Section == SectionNix {
			if s.Confinement != "" {
				t.Errorf("machine-wide row %s carries confinement %q", s.Key, s.Confinement)
			}
		}
	}
	seen := map[string]int{}
	for _, s := range rep.Stores {
		seen[s.Key]++
	}
	for key, n := range seen {
		if n > 1 {
			t.Errorf("row %s appears %d times", key, n)
		}
	}

	text := out.String()
	for _, heading := range []string{
		SectionImages + " — Podman", SectionImages + " — Apple Container",
		SectionVolumes + " — Podman", SectionVolumes + " — Apple Container",
	} {
		if strings.Count(text, heading) != 1 {
			t.Errorf("heading %q appears %d times:\n%s", heading, strings.Count(text, heading), text)
		}
	}
	if !strings.Contains(text, "Sources: Podman, Apple Container") {
		t.Errorf("the header does not name the listed sources:\n%s", text)
	}
}

// TestTheJSONNamesItsSchemaAndSources (§12.5): the document drops root `runtime` and carries
// `schema`, `sources` and `complete`, as `yolo ps` does.
func TestTheJSONNamesItsSchemaAndSources(t *testing.T) {
	o, _ := testOptions(t)
	o.Sources = []Source{podmanListed(), {Confinement: "apple-container", Display: "Apple Container",
		Setup: "apple-container-self", State: runtime.SourceUnreachable, Detail: "XPC connection error",
		NextStep: "Start it: container system start"}}
	out := new(strings.Builder)
	o.Out, o.JSON, o.NoRecord = out, true, true
	if rc := Run(o); rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schema"] != StoresSchema {
		t.Errorf("schema = %v", doc["schema"])
	}
	if _, ok := doc["runtime"]; ok {
		t.Error("the document still carries root runtime")
	}
	if doc["complete"] != false {
		t.Errorf("complete = %v with Apple Container unreachable", doc["complete"])
	}
	sources, _ := doc["sources"].([]any)
	if len(sources) != 2 {
		t.Fatalf("sources = %v", doc["sources"])
	}
	ac, _ := sources[1].(map[string]any)
	if ac["confinement"] != "apple-container" || ac["state"] != "unreachable" ||
		ac["next_step"] != "Start it: container system start" {
		t.Errorf("Apple Container's source = %v", ac)
	}
	for _, s := range doc["stores"].([]any) {
		row := s.(map[string]any)
		if strings.HasSuffix(row["key"].(string), "@apple-container") {
			t.Errorf("an unreachable source's section was collected: %v", row["key"])
		}
	}

	o.Sources = []Source{podmanListed(), {Confinement: "apple-container", State: runtime.SourceNotInstalled}}
	out.Reset()
	Run(o)
	if err := json.Unmarshal([]byte(out.String()), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["complete"] != true {
		t.Errorf("complete = %v with every source listed or not installed", doc["complete"])
	}
}

// TestALivenessJudgmentDeclinesNamingTheSourceThatDidNotAnswer (§12.5): the unused tool versions
// are judged against every source's running jails, and when a container source did not list, the
// judgment declines, naming that source and its next step, since a jail it runs may be using them.
// With every source listed, a jail any of them lists keeps its versions.
func TestALivenessJudgmentDeclinesNamingTheSourceThatDidNotAnswer(t *testing.T) {
	o, state := testOptions(t)
	now := time.Now().Add(2 * miseuse.Window)
	o.Now = func() time.Time { return now }
	o.IsMacOS = func() bool { return false }
	store := filepath.Join(state, "mise")
	writeFile(t, filepath.Join(store, "installs", "node", "20.1.0", "bin", "node"), 1000)
	since := now.Add(-miseuse.Window - 24*time.Hour).UTC().Format(time.RFC3339)
	if err := os.MkdirAll(filepath.Join(store, miseuse.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, miseuse.DirName, miseuse.SinceName), []byte(since), 0o644); err != nil {
		t.Fatal(err)
	}

	o.Sources = []Source{podmanListed(), {Confinement: "apple-container", Display: "Apple Container",
		Setup: "apple-container-self", State: runtime.SourceTimedOut,
		NextStep: "Start it: container system start"}}
	row := storeByKey(t, Inventory(o), "state.mise")
	if !strings.Contains(row.Note, "cannot tell") || !strings.Contains(row.Note, "Apple Container did not answer") ||
		!strings.Contains(row.Note, "container system start") {
		t.Errorf("with Apple Container timed out the mise row says %q; want a decline naming it and its next step", row.Note)
	}

	o.Sources = []Source{podmanListed(), appleContainerListed()}
	row = storeByKey(t, Inventory(o), "state.mise")
	if strings.Contains(row.Note, "cannot tell") || !strings.Contains(row.Note, "1 tool version(s) no jail has used") {
		t.Errorf("with every source listed the mise row says %q; want the judgment", row.Note)
	}
}

// TestNarrowedSourcesKeepTheMachineWideRows (§12.6): `yolo stores --setup` hands in one
// technology's source, and the inventory keeps every machine-wide row while collecting only that
// technology's sections.
func TestNarrowedSourcesKeepTheMachineWideRows(t *testing.T) {
	o, state := testOptions(t)
	writeFile(t, filepath.Join(state, "cache", "images", "x.tar"), 4)
	o.Sources = []Source{appleContainerListed()}
	rep := Inventory(o)
	storeByKey(t, rep, "cache.images")
	storeByKey(t, rep, "nix.install-prefix")
	storeByKey(t, rep, "images.yolo@apple-container")
	for _, s := range rep.Stores {
		if s.Confinement == "podman" {
			t.Errorf("a narrowed inventory has Podman's row %s", s.Key)
		}
	}
}
