package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// macOSRunnerPool is how many hosted macOS jobs this repository's account runs at once, Intel
// and Apple silicon together. MEASURED 2026-11-06: across the nightly-macos.yml and
// macos-user.yml runs from 41465971761 to 42228358413, with more macOS jobs queued, never
// more than five ran at the same time, in one workflow or across both. A plan change that
// raises it only makes this test conservative.
const macOSRunnerPool = 5

// The group every scheduled macOS workflow shares, so only one of them runs at a time.
const scheduledMacOSGroup = "scheduled-macos"

type budgetWorkflow struct {
	Name        string               `yaml:"name"`
	On          yaml.Node            `yaml:"on"`
	Concurrency yaml.Node            `yaml:"concurrency"`
	Jobs        map[string]budgetJob `yaml:"jobs"`
}

type budgetJob struct {
	RunsOn   yaml.Node `yaml:"runs-on"`
	Needs    yaml.Node `yaml:"needs"`
	Strategy struct {
		MaxParallel int                  `yaml:"max-parallel"`
		Matrix      map[string]yaml.Node `yaml:"matrix"`
	} `yaml:"strategy"`
}

// TestScheduledMacOSWorkLeavesCheckMacosItsRunners pins the guarantee that keeps a release
// from waiting hours for macOS runners. The release gate needs every check-macos job green on
// the exact commit, and on 2026-11-06 a push's check-macos sat queued for over an hour behind
// the scheduled macOS workflows (push run 42244051090, behind nightly run 42228358413 and
// macos-user run 42236176823), so the release request timed out on it. So scheduled macOS
// work runs one workflow at a time (one shared concurrency group), and no such workflow ever
// holds more runners than the pool minus check-macos's width. It reads the real workflow
// files: dropping a `max-parallel`, a `needs` that sequences a macOS job, or the group fails it.
func TestScheduledMacOSWorkLeavesCheckMacosItsRunners(t *testing.T) {
	wfs := readBudgetWorkflows(t)

	ci, ok := wfs["ci.yml"]
	if !ok {
		t.Fatal("ci.yml is missing; this test has lost the check-macos job it protects")
	}
	checkMacos, ok := ci.Jobs["check-macos"]
	if !ok {
		t.Fatal("ci.yml has no check-macos job, the family tools/release-gate requires")
	}
	need := jobWidth(checkMacos)
	budget := macOSRunnerPool - need
	if budget < 1 {
		t.Fatalf("check-macos runs %d jobs at once, which leaves scheduled macOS work no runner of the %d", need, macOSRunnerPool)
	}

	scheduled := map[string]bool{}
	for _, wf := range wfs {
		if triggers(wf)["schedule"] {
			scheduled[wf.Name] = true
		}
	}
	// A workflow chained after a scheduled one is scheduled work too.
	for _, wf := range wfs {
		for _, upstream := range workflowRunSources(t, wf) {
			if scheduled[upstream] {
				scheduled[wf.Name] = true
			}
		}
	}

	var checked []string
	for file, wf := range wfs {
		if !scheduled[wf.Name] || len(hostedMacOSJobs(wf)) == 0 {
			continue
		}
		checked = append(checked, file)
		group, cancel := concurrencyOf(wf)
		if !strings.Contains(group, scheduledMacOSGroup) {
			t.Errorf("%s runs scheduled work on hosted macOS but its concurrency group is %q, not %q: it would run beside the other scheduled macOS workflows and together they would take the runners check-macos needs",
				file, group, scheduledMacOSGroup)
		}
		if cancel {
			t.Errorf("%s sets cancel-in-progress in the %s group, so one scheduled run would cancel another's evidence", file, scheduledMacOSGroup)
		}
		if peak, jobs := peakMacOSJobs(wf); peak > budget {
			t.Errorf("%s can run %d hosted macOS jobs at once (%s); the budget is %d, the %d-runner pool minus check-macos's %d. Cap the matrix with `max-parallel`, or sequence the jobs with `needs`.",
				file, peak, strings.Join(jobs, ", "), budget, macOSRunnerPool, need)
		}
		for _, upstream := range workflowRunSources(t, wf) {
			if !scheduled[upstream] {
				continue
			}
			if up := byName(wfs, upstream); up == nil || !strings.Contains(groupOf(up), scheduledMacOSGroup) {
				t.Errorf("%s is chained after %q, which is not in the %s group", file, upstream, scheduledMacOSGroup)
			}
		}
	}
	sort.Strings(checked)
	for _, want := range []string{"macos-user.yml", "nightly-macos.yml", "tap-install.yml"} {
		if !contains(checked, want) {
			t.Errorf("%s is not counted as scheduled macOS work (counted: %v); the classification above has drifted", want, checked)
		}
	}
}

func readBudgetWorkflows(t *testing.T) map[string]*budgetWorkflow {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", ".github", "workflows", "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflows found (%v)", err)
	}
	out := map[string]*budgetWorkflow{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var wf budgetWorkflow
		if err := yaml.Unmarshal(data, &wf); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		out[filepath.Base(p)] = &wf
	}
	return out
}

func byName(wfs map[string]*budgetWorkflow, name string) *budgetWorkflow {
	for _, wf := range wfs {
		if wf.Name == name {
			return wf
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// triggers returns the event names under `on:`, in its string, list or map spelling.
func triggers(wf *budgetWorkflow) map[string]bool {
	out := map[string]bool{}
	switch wf.On.Kind {
	case yaml.ScalarNode:
		out[wf.On.Value] = true
	case yaml.SequenceNode:
		for _, n := range wf.On.Content {
			out[n.Value] = true
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(wf.On.Content); i += 2 {
			out[wf.On.Content[i].Value] = true
		}
	}
	return out
}

func workflowRunSources(t *testing.T, wf *budgetWorkflow) []string {
	if wf.On.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(wf.On.Content); i += 2 {
		if wf.On.Content[i].Value != "workflow_run" {
			continue
		}
		var run struct {
			Workflows []string `yaml:"workflows"`
		}
		if err := wf.On.Content[i+1].Decode(&run); err != nil {
			t.Fatalf("%s: workflow_run: %v", wf.Name, err)
		}
		return run.Workflows
	}
	return nil
}

func concurrencyOf(wf *budgetWorkflow) (group string, cancelInProgress bool) {
	switch wf.Concurrency.Kind {
	case yaml.ScalarNode:
		return wf.Concurrency.Value, false
	case yaml.MappingNode:
		var c struct {
			Group  string `yaml:"group"`
			Cancel string `yaml:"cancel-in-progress"`
		}
		_ = wf.Concurrency.Decode(&c)
		return c.Group, c.Cancel != "" && c.Cancel != "false"
	}
	return "", false
}

func groupOf(wf *budgetWorkflow) string {
	g, _ := concurrencyOf(wf)
	return g
}

// hostedMacOSJobs names the jobs that take a GitHub-hosted macOS runner: a `runs-on` label
// starting `macos-`, directly or through the matrix value it names.
func hostedMacOSJobs(wf *budgetWorkflow) []string {
	var out []string
	for id, job := range wf.Jobs {
		if job.RunsOn.Kind != yaml.ScalarNode {
			continue // a label list is a self-hosted runner (apple-container.yml)
		}
		label := job.RunsOn.Value
		if strings.HasPrefix(label, "macos-") {
			out = append(out, id)
			continue
		}
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(label, "${{ matrix."), "}}"))
		if key == label {
			continue
		}
		var values []string
		if n, ok := job.Strategy.Matrix[key]; ok {
			_ = n.Decode(&values)
		}
		if n, ok := job.Strategy.Matrix["include"]; ok {
			var include []map[string]any
			_ = n.Decode(&include)
			for _, row := range include {
				if v, ok := row[key].(string); ok {
					values = append(values, v)
				}
			}
		}
		for _, v := range values {
			if strings.HasPrefix(v, "macos-") {
				out = append(out, id)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// jobWidth is how many of a job's cells can run at once: its matrix size, capped by
// max-parallel.
func jobWidth(job budgetJob) int {
	size := 1
	for key, n := range job.Strategy.Matrix {
		if key == "exclude" {
			continue
		}
		if n.Kind == yaml.SequenceNode {
			if key == "include" && len(job.Strategy.Matrix) > 1 {
				continue // include rows extend existing cells; counted by the other keys
			}
			size *= len(n.Content)
		}
	}
	if job.Strategy.MaxParallel > 0 && job.Strategy.MaxParallel < size {
		return job.Strategy.MaxParallel
	}
	return size
}

func needsOf(job budgetJob) []string {
	switch job.Needs.Kind {
	case yaml.ScalarNode:
		return []string{job.Needs.Value}
	case yaml.SequenceNode:
		var out []string
		for _, n := range job.Needs.Content {
			out = append(out, n.Value)
		}
		return out
	}
	return nil
}

// peakMacOSJobs is the most hosted macOS jobs the workflow can run at once: the heaviest set
// of its macOS jobs no one of which (transitively) needs another.
func peakMacOSJobs(wf *budgetWorkflow) (int, []string) {
	mac := hostedMacOSJobs(wf)
	after := func(a, b string) bool { // a transitively needs b
		seen := map[string]bool{}
		stack := needsOf(wf.Jobs[a])
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == b {
				return true
			}
			if !seen[n] {
				seen[n] = true
				stack = append(stack, needsOf(wf.Jobs[n])...)
			}
		}
		return false
	}
	best, bestSet := 0, []string(nil)
	for mask := 1; mask < 1<<len(mac); mask++ {
		var set []string
		sum := 0
		for i, id := range mac {
			if mask&(1<<i) != 0 {
				set = append(set, id)
				sum += jobWidth(wf.Jobs[id])
			}
		}
		independent := true
		for i := range set {
			for j := range set {
				if i != j && after(set[i], set[j]) {
					independent = false
				}
			}
		}
		if independent && sum > best {
			best, bestSet = sum, set
		}
	}
	return best, bestSet
}
