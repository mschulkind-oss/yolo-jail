package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// setupcmd.go is `yolo setup list | show | new` (docs/design/named-setups.md §6; plan slice 3).
// A setup is a named pair of values, `confinement` (the technology that draws the walls) and
// `run_as` (the account the agent runs as); the model and every refusal text are
// internal/setup's, and where `setup` and `[setups.*]` may be written is internal/config's.
// This file only reads them and, for `new`, writes a `[setups.<name>]` table into the user
// config through the same edit path `yolo config set --user` runs (runConfigEditWithMode), so
// every user-config edit rule holds for it too.

const setupUsage = `Usage: yolo setup <list|show|new> [args]

A setup names where an agent runs: its two fields are confinement (the technology that
draws the walls: podman, apple-container, seatbelt, landlock or host) and run_as (the
account the agent runs as: self or yolo-account). yolo ships one built-in setup per
combination, named <confinement>-<run_as>; your own are [setups.<name>] tables in your
user config.

Verbs:
  list                       Every built-in and your own setups, and which one is selected and why
  show <name>                Print a setup's table, as it would be written in a config file
  new <name> --from <setup>  Copy a setup's values into a new [setups.<name>] table in your user config

The selection, first match wins: --setup, then YOLO_SETUP, then your user config's
setup line, then this OS's default (Linux podman-self, macOS apple-container-self).

Examples:
  yolo setup list
  yolo setup show seatbelt-yolo-account
  yolo setup new lab --from seatbelt-self       # then: yolo config set setup lab --user`

// setupHasBinary reports whether a technology's binary is installed here, for the
// availability note `list` gives. A variable so a test does not depend on the machine.
var setupHasBinary = func(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// setupGOOS is the OS the built-ins are judged for; a variable so a test can ask as a Mac.
var setupGOOS = goruntime.GOOS

func runSetup(args []string) int {
	return setupMain(args, os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

// setupMain is the testable body: args is the argv after the `setup` token (a leading
// `setup` is dropped, as the dispatcher passes it).
func setupMain(args []string, out, errw io.Writer, color bool) int {
	rest := args
	if len(rest) > 0 && rest[0] == "setup" {
		rest = rest[1:]
	}
	if helpRequested(rest, "--from") {
		fmt.Fprintln(out, setupUsage)
		return 0
	}
	if len(rest) == 0 {
		fmt.Fprintf(errw, "yolo setup: name a verb: list, show or new.\n\n%s\n", setupUsage)
		return 2
	}
	verb, pos := rest[0], rest[1:]
	switch verb {
	case "list":
		if len(pos) > 0 {
			fmt.Fprintf(errw, "yolo setup list: takes no arguments, got %q. Run it bare:\n  yolo setup list\n", strings.Join(pos, " "))
			return 2
		}
		return setupList(out, errw, color)
	case "show":
		if len(pos) != 1 || strings.HasPrefix(pos[0], "-") {
			fmt.Fprintf(errw, "yolo setup show: name one setup, for example:\n  yolo setup show %s\n(yolo setup list shows them all)\n", setup.Default(setupGOOS).Name)
			return 2
		}
		return setupShow(pos[0], out, errw)
	case "new":
		return setupNew(pos, out, errw)
	default:
		fmt.Fprintf(errw, "yolo setup: unknown verb %q; the verbs are list, show and new "+
			"(`yolo setup --help` says what each does).\n", verb)
		return 2
	}
}

// userSetupTables reads the user's `[setups.*]` tables: the ones that decode, and the refusal
// for each one that does not (the same refusal `yolo check` gives), sorted by name.
func userSetupTables() (map[string]setup.Setup, []string) {
	user := map[string]setup.Setup{}
	var problems []string
	raw, ok := config.UserScopeConfigOrEmpty().Get("setups")
	if !ok {
		return user, nil
	}
	tables, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return user, []string{"setups: expected a table of setups in " + paths.UserConfigDisplayPath() +
			". Remove it, then define one:\n    yolo config unset setups --user\n    yolo setup new my-setup --from " + setup.Default(setupGOOS).Name}
	}
	names := append([]string(nil), tables.Keys()...)
	sort.Strings(names)
	for _, name := range names {
		v, _ := tables.Get(name)
		m, isTable := v.(*jsonx.OrderedMap)
		if !isTable {
			problems = append(problems, fmt.Sprintf("setups.%s: expected a table with `confinement` and `run_as`. Remove it:\n    yolo config unset setups.%s --user", name, name))
			continue
		}
		fields := map[string]string{}
		for _, k := range m.Keys() {
			fv, _ := m.Get(k)
			s, _ := fv.(string)
			fields[k] = s
		}
		s, err := setup.Decode(name, fields)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		user[name] = s
	}
	return user, problems
}

// setupSummary is a setup's fields in a few words, for `list` (design §6: "Seatbelt, as you").
func setupSummary(s setup.Setup) string {
	tech := map[setup.Confinement]string{
		setup.ConfinementPodman:         "Podman",
		setup.ConfinementAppleContainer: "Apple Container",
		setup.ConfinementSeatbelt:       "Seatbelt",
		setup.ConfinementLandlock:       "Landlock",
		setup.ConfinementHost:           "No walls",
	}[s.Confinement]
	who := "as you"
	if s.RunAs == setup.RunAsYoloAccount {
		who = "as the yolo account"
	}
	return tech + ", " + who
}

func setupList(out, errw io.Writer, color bool) int {
	user, problems := userSetupTables()
	configName, _, _ := config.SelectedSetupFromConfig()
	env := os.Getenv(setup.EnvVar)
	osDefault := setup.Default(setupGOOS).Name
	// THE ONE SELECTION a launch from here makes (config.SelectSetup), the removed inputs
	// weighed as a launch weighs them: a removed YOLO_RUNTIME refuses on the host, and a
	// removed config key still chooses its built-in when nothing names a setup. The workspace
	// config is loaded loosely, as the listing verbs load it: one that does not load is none.
	var merged *jsonx.OrderedMap
	if cfg, err := config.LoadConfig("", false, func(string) {}); err == nil {
		merged = cfg
	}
	choice, selErr := config.SelectSetup(config.SetupInputs{
		Verb:      config.SetupForLaunch,
		Getenv:    os.Getenv,
		Merged:    merged,
		GOOS:      setupGOOS,
		InSession: os.Getenv("YOLO_VERSION") != "",
	})
	for _, w := range choice.Warnings {
		fmt.Fprintln(errw, w)
	}
	selected, src := choice.Setup, choice.Source

	rows := setup.BuiltIns()
	mine := make([]string, 0, len(user))
	for name := range user {
		mine = append(mine, name)
	}
	sort.Strings(mine)
	for _, name := range mine {
		rows = append(rows, user[name])
	}
	nameW, sumW := 0, 0
	for _, s := range rows {
		nameW = max(nameW, len(s.Name))
		sumW = max(sumW, len(setupSummary(s)))
	}
	for _, s := range rows {
		var notes []string
		if s.BuiltIn {
			notes = append(notes, "built-in")
		} else {
			notes = append(notes, "yours")
		}
		if env != "" && s.Name == env {
			notes = append(notes, "chosen by "+setup.EnvVar)
		}
		if configName != "" && s.Name == configName {
			notes = append(notes, "your default")
		}
		if s.Name == osDefault {
			notes = append(notes, "this OS's default")
		}
		var missing *setup.MissingTechnologyError
		if err := setup.Buildable(s, setupGOOS, setupHasBinary); errors.As(err, &missing) {
			notes = append(notes, missing.Technology+" not installed")
		} else if err != nil {
			notes = append(notes, "not available here")
		}
		name := fmt.Sprintf("%-*s", nameW, s.Name)
		if selErr == nil && s.Name == selected.Name {
			name = "[bold]" + name + "[/bold]"
		}
		line := fmt.Sprintf("%s  %-*s  %s", name, sumW, setupSummary(s), strings.Join(notes, ", "))
		fmt.Fprintln(out, richtext.Render(line, color))
	}

	rc := 0
	for _, p := range problems {
		fmt.Fprintf(errw, "%s: %s\n", paths.UserConfigDisplayPath(), p)
		rc = 1
	}
	fmt.Fprintln(out)
	switch {
	case selErr != nil:
		fmt.Fprintf(errw, "No setup is selected: %v\n", selErr)
		return 1
	case selected.Kind() == setup.KindHost:
		fmt.Fprintf(out, "Selected: %s, from %s.\n", selected.Name, choice.From())
		fmt.Fprintln(errw, setup.HostRefusal(selected))
		return 1
	case src == setup.FromDefault:
		fmt.Fprintf(out, "Selected: %s, this OS's default. Choose your own default:\n    yolo config set setup <name> --user\n", selected.Name)
	default:
		fmt.Fprintf(out, "Selected: %s, from %s.\n", selected.Name, choice.From())
	}
	if err := setup.Buildable(selected, setupGOOS, setupHasBinary); err != nil {
		var missing *setup.MissingTechnologyError
		if errors.As(err, &missing) && src == setup.FromDefault {
			fmt.Fprintln(errw, missing.AsDefault())
		} else {
			fmt.Fprintln(errw, err.Error())
		}
		return 1
	}
	return rc
}

// setupLookup finds a built-in or one of the user's setups by name, or the refusal naming
// what is defined, with a next step.
func setupLookup(name string, user map[string]setup.Setup, problems []string) (setup.Setup, error) {
	if !setup.ValidName(name) {
		return setup.Setup{}, fmt.Errorf("%q is not a setup name: a setup name is lowercase letters, digits and inner hyphens, at most 32 characters. See the defined setups:\n    yolo setup list", name)
	}
	for _, p := range problems {
		if strings.HasPrefix(p, "setups."+name+":") || strings.HasPrefix(p, "[setups."+name+"]") {
			return setup.Setup{}, fmt.Errorf("%s: %s", paths.UserConfigDisplayPath(), p)
		}
	}
	return setup.Lookup(name, user)
}

// setupTable is a setup as design §6 spells it, the way it would be written in a config file.
func setupTable(s setup.Setup) string {
	return fmt.Sprintf("[setups.%s]\nconfinement = %q\nrun_as = %q\n", s.Name, string(s.Confinement), string(s.RunAs))
}

func setupShow(name string, out, errw io.Writer) int {
	user, problems := userSetupTables()
	s, err := setupLookup(name, user, problems)
	if err != nil {
		fmt.Fprintln(errw, err.Error())
		return 1
	}
	if !s.BuiltIn {
		fmt.Fprintf(out, "# yours, in %s\n", paths.UserConfigDisplayPath())
	}
	fmt.Fprint(out, setupTable(s))
	return 0
}

const setupNewExample = "yolo setup new lab --from seatbelt-self"

func setupNew(args []string, out, errw io.Writer) int {
	var name, from string
	fromSet := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--from":
			if i+1 == len(args) {
				fmt.Fprintf(errw, "yolo setup new: --from needs the setup to copy, for example:\n  %s\n", setupNewExample)
				return 2
			}
			from, fromSet = args[i+1], true
			i++
		case strings.HasPrefix(a, "--from="):
			from, fromSet = strings.TrimPrefix(a, "--from="), true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo setup new: unknown flag %q; it takes only --from. For example:\n  %s\n", a, setupNewExample)
			return 2
		case name == "":
			name = a
		default:
			fmt.Fprintf(errw, "yolo setup new: takes one name, got %q too. For example:\n  %s\n", a, setupNewExample)
			return 2
		}
	}
	if name == "" {
		fmt.Fprintf(errw, "yolo setup new: name the new setup and the one to copy, for example:\n  %s\n(yolo setup list shows the setups to copy)\n", setupNewExample)
		return 2
	}
	if !setup.ValidName(name) {
		fmt.Fprintf(errw, "%q is not a setup name: a setup name is lowercase letters, digits and inner hyphens, at most 32 characters. For example:\n  yolo setup new %s --from %s\n",
			name, setup.SuggestName(name), orDefault(from))
		return 2
	}
	if setup.IsBuiltIn(name) {
		// Design §6, verbatim: a built-in name cannot be redefined (§8 item 2).
		_, err := setup.Decode(name, nil)
		fmt.Fprintln(errw, err.Error())
		return 1
	}
	if !fromSet || from == "" {
		fmt.Fprintf(errw, "yolo setup new: name the setup to copy with --from, for example:\n  yolo setup new %s --from %s\n(yolo setup list shows the setups to copy)\n", name, setup.Default(setupGOOS).Name)
		return 2
	}
	user, problems := userSetupTables()
	if _, exists := user[name]; exists {
		fmt.Fprintf(errw, "setup %q is already yours. See it:\n    yolo setup show %s\nOr remove it first:\n    yolo config unset setups.%s --user\n", name, name, name)
		return 1
	}
	for _, p := range problems {
		if strings.HasPrefix(p, "setups."+name+":") {
			fmt.Fprintf(errw, "%s: %s\n", paths.UserConfigDisplayPath(), p)
			return 1
		}
	}
	src, err := setupLookup(from, user, problems)
	if err != nil {
		fmt.Fprintln(errw, err.Error())
		return 1
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		// The edit path refuses a user-scope write in a jail too; this names the command that
		// was typed rather than the config edits it would run.
		fmt.Fprintf(errw, "user-scope config edits must run on the host; run: yolo host-run -C %s --name setup-new -- %s\n",
			shquote.QuoteDisplay(workspace), shquote.Join([]string{"yolo", "setup", "new", name, "--from", from}))
		return 1
	}
	for _, field := range []struct{ key, value string }{
		{"confinement", string(src.Confinement)},
		{"run_as", string(src.RunAs)},
	} {
		var editOut, editErr bytes.Buffer
		argv := []string{"set", "setups." + name + "." + field.key, field.value, "--user"}
		if rc := runConfigEditWithMode(argv, &editOut, &editErr, workspace, false); rc != 0 {
			io.Copy(errw, &editErr)
			if field.key != "confinement" {
				fmt.Fprintf(errw, "[setups.%s] was written only in part. Remove it, then run this again:\n    yolo config unset setups.%s --user\n", name, name)
			}
			return rc
		}
		io.Copy(errw, &editErr)
	}
	fmt.Fprintf(out, "%s\n%s", paths.UserConfigDisplayPath(), setupTable(setup.Setup{Name: name, Confinement: src.Confinement, RunAs: src.RunAs}))
	fmt.Fprintf(out, "Written, from %s. Make it your default:\n    yolo config set setup %s --user\n", src.Name, name)
	return 0
}

func orDefault(from string) string {
	if setup.ValidName(from) {
		return from
	}
	return setup.Default(setupGOOS).Name
}
