package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The workflow runs only on GitHub, so nothing local would notice it drifting from the
// docs it claims to follow, from the formula release.yml writes, or from this checker.
// These tests read the three files and hold them together.

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const workflowPath = ".github/workflows/tap-install.yml"

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

type workflowFile struct {
	Name        string               `yaml:"name"`
	RunName     string               `yaml:"run-name"`
	On          map[string]yaml.Node `yaml:"on"`
	Permissions map[string]string    `yaml:"permissions"`
	Jobs        map[string]struct {
		If     string         `yaml:"if"`
		RunsOn string         `yaml:"runs-on"`
		Steps  []workflowStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func parseWorkflow(t *testing.T, rel string) workflowFile {
	t.Helper()
	var wf workflowFile
	if err := yaml.Unmarshal([]byte(repoFile(t, rel)), &wf); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return wf
}

// docsFormula is the tap-qualified formula the install docs tell a user to run. Every
// `brew install <owner>/<tap>/yolo-jail` in README.md and the getting-started guide
// must name the same one, and each file must name it at least once.
func docsFormula(t *testing.T) string {
	t.Helper()
	re := regexp.MustCompile(`brew install ([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+/yolo-jail)\b`)
	var found []string
	for _, doc := range []string{"README.md", "userguide/getting-started.md"} {
		ms := re.FindAllStringSubmatch(repoFile(t, doc), -1)
		if len(ms) == 0 {
			t.Fatalf("%s no longer says `brew install <tap>/yolo-jail`; this job verifies the command it gives", doc)
		}
		for _, m := range ms {
			found = append(found, m[1])
		}
	}
	slices.Sort(found)
	if found = slices.Compact(found); len(found) != 1 {
		t.Fatalf("the install docs name %d different formulae %v; they must agree before this job can verify one", len(found), found)
	}
	return found[0]
}

// TestTheWorkflowInstallsWhatTheDocsSay pins the job's commands, their order, and the
// call into this checker. It fails if the checker's step is deleted or loses the
// event-derived expectation, if the install stops being the docs' command verbatim, or
// if the checkout moves ahead of the install it must not influence.
func TestTheWorkflowInstallsWhatTheDocsSay(t *testing.T) {
	formula := docsFormula(t)
	wf := parseWorkflow(t, workflowPath)
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s has %d jobs, want the one tap-install job", workflowPath, len(wf.Jobs))
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	checker := "go run ./tools/" + filepath.Base(wd)

	for id, job := range wf.Jobs {
		if !strings.HasPrefix(job.RunsOn, "macos-") {
			t.Errorf("job %s runs on %q; the tap install is verified on a GitHub-hosted Mac", id, job.RunsOn)
		}
		at := func(match func(workflowStep) bool, what string) int {
			for i, s := range job.Steps {
				if match(s) {
					return i
				}
			}
			t.Errorf("job %s has no step that %s", id, what)
			return -1
		}
		install := at(func(s workflowStep) bool { return strings.TrimSpace(s.Run) == "brew install "+formula },
			"runs `brew install "+formula+"`, the docs' command")
		test := at(func(s workflowStep) bool { return strings.TrimSpace(s.Run) == "brew test "+formula },
			"runs `brew test "+formula+"`")
		verify := at(func(s workflowStep) bool {
			f := strings.Fields(s.Run)
			return strings.HasPrefix(strings.TrimSpace(s.Run), checker) &&
				slices.Contains(f, "-expect-from-github-event") &&
				slices.Index(f, "-formula") >= 0 && slices.Index(f, "-formula")+1 < len(f) &&
				f[slices.Index(f, "-formula")+1] == formula
		}, "runs `"+checker+" -formula "+formula+" -expect-from-github-event`")
		checkout := at(func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "actions/checkout@") },
			"checks out the repository for the checker")
		setupGo := at(func(s workflowStep) bool { return strings.HasPrefix(s.Uses, "actions/setup-go@") },
			"installs Go for the checker")
		if install < 0 || test < 0 || verify < 0 || checkout < 0 || setupGo < 0 {
			continue
		}
		if !(install < test && test < verify) {
			t.Errorf("job %s runs install, test and verify at steps %d, %d, %d; want them in that order", id, install, test, verify)
		}
		if checkout < test || setupGo < test || verify < checkout || verify < setupGo {
			t.Errorf("job %s checks out at step %d and sets up Go at %d; both belong after the install "+
				"and its test (%d, %d), so neither can influence them, and before the checker (%d)",
				id, checkout, setupGo, install, test, verify)
		}
		if got := job.Steps[checkout].With["persist-credentials"]; got != "false" {
			t.Errorf("job %s's checkout sets persist-credentials %q; the job needs no token left in the checkout", id, got)
		}
		// The checker refuses a failed release's payload too; the `if:` is what keeps
		// such a run from starting a Mac for nothing.
		if !strings.Contains(job.If, "github.event.workflow_run.conclusion == 'success'") {
			t.Errorf("job %s has if %q; a workflow_run from a failed release must be skipped", id, job.If)
		}
	}
}

// TestTheWorkflowRunsAfterEachReleaseWeeklyAndByHand pins the triggers, including the
// one link a rename would break silently: workflow_run names release.yml by its `name:`.
func TestTheWorkflowRunsAfterEachReleaseWeeklyAndByHand(t *testing.T) {
	wf := parseWorkflow(t, workflowPath)
	release := parseWorkflow(t, ".github/workflows/release.yml")

	var run struct {
		Workflows []string `yaml:"workflows"`
		Types     []string `yaml:"types"`
	}
	if node, ok := wf.On["workflow_run"]; !ok {
		t.Error("no workflow_run trigger: the job must run once each release has pushed its formula")
	} else if err := node.Decode(&run); err != nil {
		t.Error(err)
	}
	if !slices.Contains(run.Workflows, release.Name) {
		t.Errorf("workflow_run follows %v, but release.yml is named %q", run.Workflows, release.Name)
	}
	if !slices.Equal(run.Types, []string{"completed"}) {
		t.Errorf("workflow_run types %v, want [completed]", run.Types)
	}

	var schedule []struct {
		Cron string `yaml:"cron"`
	}
	if node, ok := wf.On["schedule"]; !ok || node.Decode(&schedule) != nil || len(schedule) != 1 {
		t.Error("want exactly one schedule: the weekly run that catches Homebrew, Go and macOS moving without a release")
	} else if f := strings.Fields(schedule[0].Cron); len(f) != 5 || f[2] != "*" || f[3] != "*" || f[4] == "*" {
		t.Errorf("schedule %q is not weekly", schedule[0].Cron)
	}

	var dispatch struct {
		Inputs map[string]struct {
			Required bool `yaml:"required"`
		} `yaml:"inputs"`
	}
	if node, ok := wf.On["workflow_dispatch"]; !ok || node.Decode(&dispatch) != nil {
		t.Error("no workflow_dispatch trigger")
	} else if in, ok := dispatch.Inputs["version"]; !ok || in.Required {
		t.Errorf("workflow_dispatch wants an optional `version` input (expectFromGitHubEvent reads it); got %+v", dispatch.Inputs)
	}
	for trigger := range wf.On {
		if !slices.Contains([]string{"workflow_run", "schedule", "workflow_dispatch"}, trigger) {
			t.Errorf("unexpected trigger %q; expectFromGitHubEvent knows only workflow_run, schedule and workflow_dispatch", trigger)
		}
	}
}

// TestTheWorkflowCarriesNoSecrets: the install is anonymous, as a user's is, and the
// job only reads.
func TestTheWorkflowCarriesNoSecrets(t *testing.T) {
	body := repoFile(t, workflowPath)
	if strings.Contains(body, "secrets.") || strings.Contains(body, "github.token") {
		t.Errorf("%s references a secret or token; the tap install needs none", workflowPath)
	}
	wf := parseWorkflow(t, workflowPath)
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("permissions %v, want exactly {contents: read}", wf.Permissions)
	}
}

// TestReleasePushesTheFormulaTheDocsInstall holds the release helper to the formula
// the docs install. Homebrew maps owner/tap to github.com/owner/homebrew-tap and the
// formula name to Formula/<name>.rb; its test block is the production version check.
func TestReleasePushesTheFormulaTheDocsInstall(t *testing.T) {
	parts := strings.Split(docsFormula(t), "/")
	owner, tap, name := parts[0], parts[1], parts[2]
	helper := repoFile(t, "tools/release-wiring/update-homebrew.sh")
	for _, want := range []string{
		"github.com/" + owner + "/homebrew-" + tap + ".git",
		"tap/Formula/" + name + ".rb",
		"test do",
		`assert_match "` + versionLinePrefix + `#{version}", shell_output("#{bin}/yolo --version")`,
	} {
		if !strings.Contains(helper, want) {
			t.Errorf("trusted Homebrew update helper no longer says %q", want)
		}
	}
}

// Render the actual Actions format() call rather than inventing an unrelated
// parser title. This pins inputs/order/grammar through the production checker.
func TestReleaseRunNameTransportReachesTheProductionTapCaller(t *testing.T) {
	release := parseWorkflow(t, ".github/workflows/release.yml")
	re := regexp.MustCompile(`format\('([^']+)', inputs\.version, inputs\.sha, inputs\.request_run_id\)`)
	match := re.FindStringSubmatch(release.RunName)
	if len(match) != 2 {
		t.Fatalf("normal Release lost its real version/SHA/request format() call: %q", release.RunName)
	}
	for _, version := range []string{"0.11.0", "0.12.0", "0.12.0-rc.1"} {
		t.Run(version, func(t *testing.T) {
			title := strings.NewReplacer("{0}", version, "{1}", strings.Repeat("a", 40), "{2}", "123").Replace(match[1])
			m := newStubMachine(t)
			m.install(t)
			payload := filepath.Join(m.root, "event.json")
			data, err := json.Marshal(map[string]any{"workflow_run": map[string]string{"name": release.Name, "event": "workflow_dispatch", "head_branch": "main", "conclusion": "success", "display_title": title}})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, payload, string(data), 0o600)
			t.Setenv("GITHUB_EVENT_NAME", "workflow_run")
			t.Setenv("GITHUB_EVENT_PATH", payload)
			rc, out := runChecker(t, "-formula", testFormula, "-expect-from-github-event")
			if version == "0.11.0" {
				if rc != 0 {
					t.Fatalf("actual run-name/checker match failed: %s", out)
				}
			} else if rc != 1 || !strings.Contains(out, "but this run verifies release "+version) {
				t.Fatalf("actual production title waived exact version: rc=%d title=%q output=%s", rc, title, out)
			}
		})
	}
}
