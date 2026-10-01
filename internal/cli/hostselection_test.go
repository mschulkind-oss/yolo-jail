package cli

// hostselection_test.go pins notch-convergence item 6 at the host: one selection function, the
// closure included, at every host verb (row B1), and NC-D5's dispositions for a selection that is
// not what the config asks for (row B4).

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// claudeAndItsNeeds is what `"packs": ["claude"]` selects at every notch: claude, the three
// packs its unconditional `needs` joins, and aws-auth, which bedrock's need joins after them.
var claudeAndItsNeeds = []string{"claude", "bedrock", "openai-auth", "wire-bridge", "aws-auth"}

// claudeDirectNeeds and bedrockNeeds say which pack's need joins each of them, the pack a
// cause line names.
var (
	claudeDirectNeeds = []string{"bedrock", "openai-auth", "wire-bridge"}
	bedrockNeeds      = []string{"aws-auth"}
)

func packNames(packs []*packload.Pack) []string {
	out := make([]string, 0, len(packs))
	for _, p := range packs {
		out = append(out, p.Name)
	}
	return out
}

// selectionHome is a scratch home whose user config is cfg.
func selectionHome(t *testing.T, cfg string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, cfg)
	return home
}

// EVERY HOST VERB'S OWN READER composes the packs a jail launch stages for `["claude"]`, in the
// jail's order: the configured pack, then the closure's additions. Each row calls the verb's own
// function, so a verb that stops calling the one selection function fails its row.
func TestClaudeAloneSelectsTheLaunchClosureAtEveryHostVerb(t *testing.T) {
	selectionHome(t, claudeAlone)
	fold, _ := loadPromoteFold()
	inspected, _ := configuredPacksForInspection()
	for _, tc := range []struct {
		verb  string
		packs []*packload.Pack
	}{
		{"yolo host -- claude / yolo host env", loadedHostPacks(config.UserScopeConfigOrEmpty(), "claude", "").packs},
		{"the host footer", footerHostPacks()},
		{"the config inspection verbs", inspected},
		{"config promote's fold", fold.packs},
		{"host apply, capture, revert and check-deps", selectConfiguredHostPacks().packs},
	} {
		if got := packNames(tc.packs); !slices.Equal(got, claudeAndItsNeeds) {
			t.Errorf("%s selects %v, want %v (what a jail launch stages)", tc.verb, got, claudeAndItsNeeds)
		}
	}
	if fold.afterConfigured != 1 {
		t.Errorf("config promote's fold puts the local pack's slot at %d, want 1: after the "+
			"configured entries and before the closure's additions", fold.afterConfigured)
	}
	// The one host verb that reads the shipped set on top: every one of them is a candidate.
	for _, name := range claudeAndItsNeeds {
		if !slices.Contains(packNames(hostRevertCandidates(&bytes.Buffer{})), name) {
			t.Errorf("revert does not consider %s", name)
		}
	}
}

// `yolo host apply` names the packs the closure joined, and renders from the complete set.
func TestHostApplyNamesThePacksTheClosureJoined(t *testing.T) {
	selectionHome(t, claudeAlone)
	var out, errw bytes.Buffer
	if rc := applyHostSurveyed(&out, &errw, false, false, nil, &hostApplySurvey{}); rc != 0 {
		t.Fatalf("host apply dry run rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	for needy, names := range map[string][]string{"claude": claudeDirectNeeds, "bedrock": bedrockNeeds} {
		for _, name := range names {
			want := "joined — + " + name + " (needed by " + needy + ")"
			if !strings.Contains(out.String(), want) {
				t.Errorf("host apply must say %q:\n%s", want, out.String())
			}
		}
	}
}

// THE LAUNCH ANNOUNCES EVERY JOINED PACK (WB-D12), at both host front doors, before the exec.
func TestHostLaunchAnnouncesThePacksTheClosureJoined(t *testing.T) {
	_, errs := hostGateLaunchWith(t, claudeAlone, nil, nil, "claude")
	hostGateHome(t, claudeAlone, nil)
	var out, envErr bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude"}, &out, &envErr, false, nil); rc != 0 {
		t.Fatalf("yolo host env rc=%d\n%s", rc, envErr.String())
	}
	for needy, names := range map[string][]string{"claude": claudeDirectNeeds, "bedrock": bedrockNeeds} {
		for _, name := range names {
			if want := "yolo host: + " + name + " (needed by " + needy + ")"; !strings.Contains(errs, want) {
				t.Errorf("yolo host -- claude must print %q:\n%s", want, errs)
			}
			if want := "yolo host env: + " + name + " (needed by " + needy + ")"; !strings.Contains(envErr.String(), want) {
				t.Errorf("yolo host env must print %q:\n%s", want, envErr.String())
			}
		}
	}
	if strings.Contains(out.String(), "needed by") {
		t.Errorf("a cause line reached the script a shell evals:\n%s", out.String())
	}
}

// THE DONE-WHEN'S SECOND HALF, over every launch the plan names and at both front doors: no host
// launch of `["claude"]` exports an address nothing at the host serves, with or without bedrock.
// aws-auth's pointer is what ES-D24 measured the closure adding; it is withheld and named.
func TestNoHostLaunchOfClaudeExportsAnUnservedPointer(t *testing.T) {
	// The shell this test runs in may itself be a jail's, carrying these; blank them so what the
	// agent receives is what yolo composed.
	for _, k := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"CODEX_REFRESH_TOKEN_URL_OVERRIDE"} {
		t.Setenv(k, "")
	}
	for _, flags := range [][]string{nil, {"-p", "bedrock"}} {
		env, errs := hostGateLaunchWith(t, claudeAlone, nil, flags, "claude")
		for k, v := range env {
			if v == "" || os.Getenv(k) == v {
				continue // inherited, not composed
			}
			if strings.HasPrefix(k, "AWS_CONTAINER_") || strings.Contains(v, "127.0.0.1:146") {
				t.Errorf("yolo host %v -- claude exported %s=%q, which only a jail daemon serves\n%s",
					flags, k, v, errs)
			}
		}
	}
	hostGateHome(t, claudeAlone, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "bedrock"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env rc=%d\n%s", rc, errw.String())
	}
	if hostExportsPrefix(out.String(), "AWS_CONTAINER_") != "" {
		t.Errorf("yolo host env -p bedrock exported aws-auth's pointer:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Errorf("the withheld pointer must be named:\n%s", errw.String())
	}
}

// NC-D5 AT THE LAUNCH-SHAPED VERBS: a malformed `packs` entry REFUSES `yolo host --` and `yolo
// host env`, naming the entry, as config validation refuses every jail launch over it. It used to
// be dropped with no word, and the launch ran without the pack the user meant.
func TestHostLaunchRefusesAMalformedPacksEntry(t *testing.T) {
	const cfg = `{"packs": ["claude", 42]}`
	rc, env, errs := hostGateRun(t, cfg, nil, nil, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("yolo host -- claude over a malformed entry must refuse before the exec: rc=%d\n%s", rc, errs)
	}
	hostGateHome(t, cfg, nil)
	var out, envErr bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude"}, &out, &envErr, false, nil); rc == 0 || out.Len() != 0 {
		t.Fatalf("yolo host env over a malformed entry must refuse, printing nothing to eval: rc=%d\n%s",
			rc, out.String())
	}
	for _, got := range []string{errs, envErr.String()} {
		for _, want := range []string{"config.packs[1]", "a jail launch refuses the same config",
			"remove it from `packs`"} {
			if !strings.Contains(got, want) {
				t.Errorf("the refusal must say %q:\n%s", want, got)
			}
		}
	}
}

// NC-D5 AT A WRITER: a `packs` list whose entries are all malformed is NOT an empty `packs`.
// `yolo host apply` used to read it as one and take the branch that retires every pack's output;
// it now reports the entry, and --assert refuses the incomplete set and writes nothing.
func TestHostApplyRefusesAPacksListOfOnlyMalformedEntries(t *testing.T) {
	home := selectionHome(t, `{"packs": [42]}`)
	before := hashTree(t, home)
	var out, errw bytes.Buffer
	rc := applyHostSurveyed(&out, &errw, false, true, nil, &hostApplySurvey{})
	report := out.String() + errw.String()
	if rc == 0 {
		t.Fatalf("host apply --assert over a malformed entry must refuse:\n%s", report)
	}
	if strings.Contains(report, "No packs configured") {
		t.Errorf("a malformed entry was read as an empty `packs`:\n%s", report)
	}
	if !strings.Contains(report, "config.packs[0]") || !strings.Contains(report, "Nothing was written") {
		t.Errorf("the refusal must name the entry and say nothing was written:\n%s", report)
	}
	if hashTree(t, home) != before {
		t.Errorf("host apply wrote into the home over an incomplete set:\n%s", report)
	}
}

// NC-D5 AT A READ-ONLY VERB: the config inspection verbs report a malformed entry beside the
// packs they could not resolve, rather than describing a narrower set in silence.
func TestConfigInspectionReportsAMalformedPacksEntry(t *testing.T) {
	selectionHome(t, `{"packs": ["claude", 42]}`)
	packs, problems := configuredPacksForInspection()
	if !slices.Equal(packNames(packs), claudeAndItsNeeds) {
		t.Errorf("the resolvable part still selects %v, want %v", packNames(packs), claudeAndItsNeeds)
	}
	if len(problems) != 1 || problems[0].Name != "config.packs[1]" {
		t.Fatalf("problems = %+v, want the malformed entry named by its place", problems)
	}
	// And by the file and line it was written at, since its place is in the composed list.
	if want := "(written at ~/.config/yolo-jail/config.jsonc:1:22)"; !strings.HasSuffix(problems[0].Reason, want) {
		t.Errorf("reason = %q, want it to end %q", problems[0].Reason, want)
	}
}

// THE CALL SITES, by source: no host verb resolves `packs` entries itself any more. Every
// production reader of resolveConfiguredPack or config.LoadPacks in this package is either the
// one selection function or reads entries, not a selection (`yolo pack` verbs list them,
// `describe` names them, `config promote --to pack:` finds a destination by name,
// `init-user-config` asks whether the user config names any).
func TestEveryHostVerbSelectsThroughTheOneFunction(t *testing.T) {
	allowed := map[string][]string{
		// Its definition, and packForCheckDeps; every verb hands it to selectHostPacks as a value.
		`resolveConfiguredPack\(`: {"checkdeps.go"},
		`config\.LoadPacks\(`:     {"pack.go", "describe.go", "configpromotewrite.go", "init.go"},
		// A selection closed by hand: `packload.Selection{…}.Close(…)`.
		`\)\.Close\(|\}\.Close\(`: nil,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for pattern, ok := range allowed {
			if regexp.MustCompile(pattern).MatchString(src) && !slices.Contains(ok, f) {
				t.Errorf("%s calls %s; a host verb selects packs through selectHostPacks", f, pattern)
			}
		}
	}
}
