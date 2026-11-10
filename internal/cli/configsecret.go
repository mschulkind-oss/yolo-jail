package cli

// configsecret.go is `yolo config secret|unsecret` (docs/design/secret-env-scoping.md SE-D9,
// §3.5; secret-env-scoping-plan.md Task 5): it moves one name, or with --dotenv one dotenv file
// entry, between the public set ([env]) and the private set ([env.secrets]) of the scope that
// holds it. An inline value moves to the other set's `set` table; a dotenv line moves verbatim to
// the first file the other set lists in that scope (SEP-D12). The destination is written first,
// then the source removed (SEP-D10), so a failure between the two leaves both copies, never none.
//
// NEITHER VERB PRINTS A VALUE. That is why they do not go through applyConfigEdit, whose hunks
// carry the edited line: they print one `moved NAME from <place> to <place>` line instead
// (SEP-D7), and scrub any value an edit's error might quote.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const configSecretUsage = "usage: yolo config secret <NAME> [--drop] [--user|--project|--local|--file PATH]\n" +
	"       yolo config unsecret <NAME> [--drop] [--user|--project|--local|--file PATH]\n" +
	"       yolo config secret --dotenv <PATH> [--user|--project|--local|--file PATH]\n" +
	"       yolo config unsecret --dotenv <PATH> [--user|--project|--local|--file PATH]"

// secretMove is one parsed invocation.
type secretMove struct {
	verb    string // "secret" or "unsecret"
	name    string
	dotenv  string
	drop    bool
	scope   string // a scope flag, "" for none
	file    string // --file PATH
	argv    []string
	private bool // the destination set
}

func (m secretMove) setWord(private bool) string {
	if private {
		return "[env.secrets]"
	}
	return "[env]"
}

// command is this verb again, for a refusal's next step, with scope as its scope flag.
func (m secretMove) command(scope string) string {
	parts := []string{"yolo", "config", m.verb}
	if m.dotenv != "" {
		parts = append(parts, "--dotenv", m.dotenv)
	} else {
		parts = append(parts, m.name)
	}
	if m.drop {
		parts = append(parts, "--drop")
	}
	if scope != "" {
		parts = append(parts, scope)
	}
	return shquote.Join(parts)
}

func runConfigSecret(args []string, out, errw io.Writer, workspace string) int {
	m := secretMove{verb: args[0], argv: args, private: args[0] == "secret"}
	var positionals []string
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "--user", "--project", "--local":
			if m.scope != "" || m.file != "" {
				fmt.Fprintln(errw, "choose only one of --user, --project, --local, or --file")
				return 2
			}
			m.scope = a
		case "--file", "--dotenv":
			if i+1 == len(args) {
				fmt.Fprintf(errw, "%s requires one path\n%s\n", a, configSecretUsage)
				return 2
			}
			if a == "--dotenv" {
				m.dotenv = args[i+1]
			} else {
				if m.scope != "" || m.file != "" {
					fmt.Fprintln(errw, "choose only one of --user, --project, --local, or --file")
					return 2
				}
				m.file = args[i+1]
			}
			i++
		case "--drop":
			m.drop = true
		case "--help", "-h":
			fmt.Fprintln(out, configSecretUsage)
			return 0
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(errw, "unknown flag %q\n%s\n", a, configSecretUsage)
				return 2
			}
			positionals = append(positionals, a)
		}
	}
	switch {
	case m.dotenv != "" && (len(positionals) != 0 || m.drop):
		fmt.Fprintf(errw, "--dotenv moves a whole file entry, so it takes no NAME and no --drop\n%s\n", configSecretUsage)
		return 2
	case m.dotenv == "" && len(positionals) != 1:
		fmt.Fprintf(errw, "yolo config %s takes exactly one variable name\n%s\n", m.verb, configSecretUsage)
		return 2
	}
	if m.dotenv == "" {
		m.name = positionals[0]
		if !config.IsValidEnvName(m.name) {
			fmt.Fprintf(errw, "yolo config %s: %q is not a variable name (letters, digits and _, not starting with a digit)\n", m.verb, m.name)
			return 2
		}
	}
	if config.InJail() && m.scope == "--user" {
		printConfigEditHostRun(errw, workspace, args)
		return 1
	}
	files, err := config.ReadEnvScopeFiles(workspace, false)
	if err != nil {
		fmt.Fprintf(errw, "yolo config %s: %v; fix that file, then run this command again\n", m.verb, err)
		return 1
	}
	all := files
	if m.file != "" {
		scope, ok := scopeOfConfigFile(files, m.file)
		if !ok {
			fmt.Fprintf(errw, "yolo config %s: --file %s names no user, project or local config; use --user, --project or --local\n", m.verb, m.file)
			return 2
		}
		m.scope = scope
	}
	if m.scope != "" {
		var kept []config.EnvScopeFile
		for _, f := range files {
			if f.Scope == m.scope {
				kept = append(kept, f)
			}
		}
		files = kept
	}
	if m.dotenv != "" {
		return m.moveDotenvEntry(files, workspace, out, errw)
	}
	return m.moveName(files, all, workspace, out, errw)
}

func scopeOfConfigFile(files []config.EnvScopeFile, file string) (string, bool) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", false
	}
	for _, f := range files {
		if filepath.Clean(f.ConfigFile) == filepath.Clean(abs) {
			return f.Scope, true
		}
	}
	return "", false
}

// refuseLegacy names `yolo config migrate` first when f is a JSONC file (SE-D6, SEP-D18).
func (m secretMove) refuseLegacy(f config.EnvScopeFile, errw io.Writer) bool {
	if !f.Legacy {
		return false
	}
	fmt.Fprintf(errw, "yolo config %s edits TOML config only, and %s is in the old format; run yolo config migrate, then %s\n",
		m.verb, f.ConfigFile, m.command(f.Scope))
	return true
}

// inJailUserScope prints the host-run line for a user-scope edit from inside a jail.
func (m secretMove) inJailUserScope(f config.EnvScopeFile, workspace string, errw io.Writer) bool {
	if !config.InJail() || f.Scope != config.EnvScopeUser {
		return false
	}
	argv := append([]string(nil), m.argv...)
	if m.scope == "" {
		argv = append(argv, "--user")
	}
	printConfigEditHostRun(errw, workspace, argv)
	return true
}

// moveName moves one name in the scope files selects; all is every scope's file, which the
// destination set is looked up in, so a copy another scope holds is never promoted over or
// duplicated.
func (m secretMove) moveName(files, all []config.EnvScopeFile, workspace string, out, errw io.Writer) int {
	type held struct {
		file        config.EnvScopeFile
		source, dst []config.EnvSite
	}
	var holding []held
	var anyDest []config.EnvSite
	for _, f := range all {
		for _, s := range f.Sites(m.name) {
			if s.Place.Private == m.private {
				anyDest = append(anyDest, s)
			}
		}
	}
	for _, f := range files {
		h := held{file: f}
		for _, s := range f.Sites(m.name) {
			if s.Place.Private == m.private {
				h.dst = append(h.dst, s)
			} else {
				h.source = append(h.source, s)
			}
		}
		if len(h.source) > 0 {
			holding = append(holding, h)
		}
	}
	from, to := m.setWord(!m.private), m.setWord(m.private)
	switch len(holding) {
	case 0:
		if len(anyDest) > 0 {
			fmt.Fprintf(errw, "%s is already only in %s %s; nothing to move\n", m.name, to, anyDest[0].Place.Describe())
			return 0
		}
		fmt.Fprintf(errw, "%s is set in no config's %s, so there is nothing to move", m.name, from)
		if m.private {
			fmt.Fprintf(errw, "; to set it privately, add a line %s=<value> to a file [env.secrets] files lists "+
				"(yolo config add env.secrets.files '\"~/.config/yolo-jail/secrets.env\"' --user adds one)", m.name)
		} else {
			fmt.Fprintf(errw, "; to set it publicly, add a line %s=<value> to a file [env] files lists", m.name)
		}
		fmt.Fprintln(errw)
		return 1
	case 1:
	default:
		fmt.Fprintf(errw, "%s is set in %s by more than one config; run one of these:\n", m.name, from)
		for _, h := range holding {
			fmt.Fprintf(errw, "  %s\n", m.command(h.file.Scope))
		}
		return 1
	}
	h := holding[0]
	if m.refuseLegacy(h.file, errw) || m.inJailUserScope(h.file, workspace, errw) {
		return 1
	}
	if len(h.source) > 1 {
		fmt.Fprintf(errw, "%s is set in %s more than once in %s:", m.name, from, h.file.ConfigFile)
		for _, s := range h.source {
			fmt.Fprintf(errw, " %s", s.Place.Describe())
		}
		fmt.Fprintf(errw, "; keep one of them by hand, then run %s again\n", m.command(m.scope))
		return 1
	}
	src := h.source[0]
	// A legacy env_sources entry never gets here: its file is JSONC, refused above (SE-D6).
	// The copy a move keeps or collides with: this file's, else another scope's.
	kept := config.EnvSite{}
	switch {
	case len(h.dst) > 0:
		kept = h.dst[0]
	case len(anyDest) > 0:
		kept = anyDest[0]
	}
	if !src.Inline() && m.refuseLinked(src.Dotenv, errw) {
		return 1
	}
	if m.drop {
		if kept.Place.Key == "" {
			fmt.Fprintf(errw, "--drop keeps the %s copy of %s, but no config holds one; run %s to move it instead\n",
				to, m.name, shquote.Join(dropless(m.argv)))
			return 1
		}
		if !kept.Inline() && !src.Inline() && (kept.Dotenv == src.Dotenv || config.SameDotenvFile(kept.Dotenv, src.Dotenv)) {
			fmt.Fprintf(errw, "%s lists %s in both sets, so --drop would delete its only copy of %s; "+
				"put the file in one set first with %s\n", m.name, src.Dotenv, m.name, m.dotenvCommand(src.Raw, h.file.Scope))
			return 1
		}
		if rc := m.removeSite(h.file, src, workspace, errw); rc != 0 {
			return rc
		}
		fmt.Fprintf(out, "dropped %s from %s; kept %s in the %s config\n", m.name, src.Place.Describe(),
			kept.Place.Describe(), strings.TrimPrefix(kept.Scope, "--"))
		return m.approveLine(h.file, out)
	}
	if kept.Place.Key != "" {
		fmt.Fprintf(errw, "%s is already in %s %s too; keep the %s copy with %s, or the %s one with %s\n",
			m.name, to, kept.Place.Describe(), to, m.withDrop(h.file.Scope), from, m.inverseDrop(kept.Scope))
		return 1
	}
	var dest config.EnvPlace
	if src.Inline() {
		value, ok := h.file.InlineValue(src, m.name)
		if !ok {
			fmt.Fprintf(errw, "%s in %s is not a string; edit it by hand, then run yolo check\n", m.name, h.file.ConfigFile)
			return 1
		}
		destKey := "env.set"
		if m.private {
			destKey = "env.secrets.set"
		}
		if rc := m.edit(h.file, workspace, configedit.Set, append(stepsOf(destKey), configdoc.Step{Key: m.name}), value, errw); rc != 0 {
			return rc
		}
		dest = config.EnvPlace{Private: m.private, Key: destKey}
	} else {
		entries := h.file.FileEntries(m.private)
		if len(entries) == 0 {
			fmt.Fprintf(errw, "no %s file to move %s into; run yolo config add %s %s %s, then this command again\n",
				privateWord(m.private), m.name, filesKey(m.private), shquote.Quote(`"`+suggestedDotenv(h.file, m.private)+`"`), h.file.Scope)
			return 1
		}
		target := entries[0]
		if m.refuseLinked(target.Dotenv, errw) {
			return 1
		}
		line, found, err := config.DotenvLine(src.Dotenv, m.name)
		if err != nil || !found {
			fmt.Fprintf(errw, "cannot read %s's line from %s: %v; check the file, then run this command again\n", m.name, src.Dotenv, err)
			return 1
		}
		if err := config.AppendDotenvLine(target.Dotenv, line); err != nil {
			fmt.Fprintf(errw, "cannot write %s: %v; nothing was moved\n", target.Dotenv, scrubPathErr(err))
			return 1
		}
		if _, _, err := config.RemoveDotenvName(src.Dotenv, m.name); err != nil {
			fmt.Fprintf(errw, "%s was copied to %s, but removing it from %s failed: %v; remove its line there by hand\n",
				m.name, target.Dotenv, src.Dotenv, scrubPathErr(err))
			return 1
		}
		fmt.Fprintf(out, "moved %s from %s to %s\n", m.name, src.Place.Describe(), target.Place.Describe())
		return 0
	}
	if rc := m.removeSite(h.file, src, workspace, errw); rc != 0 {
		return rc
	}
	fmt.Fprintf(out, "moved %s from %s to %s\n", m.name, src.Place.Describe(), dest.Describe())
	return m.approveLine(h.file, out)
}

// removeSite removes one site: a `set` table's key, or every line of a dotenv file that sets name.
func (m secretMove) removeSite(f config.EnvScopeFile, s config.EnvSite, workspace string, errw io.Writer) int {
	if s.Inline() {
		path := stepsOf(s.Place.Key)
		if s.Place.Key == "env.sources" {
			// SEP-D13: one name of an inline sources table, env.sources[i].set.NAME.
			path = append(path, configdoc.Step{Index: s.Index, IsIndex: true}, configdoc.Step{Key: "set"})
		}
		return m.edit(f, workspace, configedit.Remove, append(path, configdoc.Step{Key: m.name}), "", errw)
	}
	if _, _, err := config.RemoveDotenvName(s.Dotenv, m.name); err != nil {
		fmt.Fprintf(errw, "cannot remove %s from %s: %v; remove its line there by hand\n", m.name, s.Dotenv, scrubPathErr(err))
		return 1
	}
	return 0
}

// moveDotenvEntry moves a dotenv file's entry from one set's file list to the other's in the
// scope that lists it (secret-env-scoping-plan.md SEP-D17).
func (m secretMove) moveDotenvEntry(files []config.EnvScopeFile, workspace string, out, errw io.Writer) int {
	type listing struct {
		file  config.EnvScopeFile
		entry config.EnvSite
	}
	var found []listing
	for _, f := range files {
		for _, e := range f.FileEntries(!m.private) {
			if sameDotenv(e, m.dotenv, f.Anchor) {
				found = append(found, listing{f, e})
				break
			}
		}
	}
	switch len(found) {
	case 0:
		fmt.Fprintf(errw, "no config lists %s in %s files, so there is nothing to move; `yolo config why env` shows where env is set\n",
			m.dotenv, m.setWord(!m.private))
		return 1
	case 1:
	default:
		fmt.Fprintf(errw, "%s is listed in %s files by more than one config; run one of these:\n", m.dotenv, m.setWord(!m.private))
		for _, l := range found {
			fmt.Fprintf(errw, "  %s\n", m.command(l.file.Scope))
		}
		return 1
	}
	l := found[0]
	if m.refuseLegacy(l.file, errw) || m.inJailUserScope(l.file, workspace, errw) {
		return 1
	}
	already := false
	for _, e := range l.file.FileEntries(m.private) {
		if sameDotenv(e, l.entry.Raw, l.file.Anchor) {
			already = true
		}
	}
	if !already {
		if rc := m.edit(l.file, workspace, configedit.Append, stepsOf(filesKey(m.private)), l.entry.Raw, errw); rc != 0 {
			return rc
		}
	}
	removePath := append(stepsOf(l.entry.Place.Key), configdoc.Step{Index: l.entry.Index, IsIndex: true})
	if rc := m.edit(l.file, workspace, configedit.Remove, removePath, "", errw); rc != 0 {
		return rc
	}
	dest := config.EnvPlace{Private: m.private, Key: filesKey(m.private), Entry: l.entry.Place.Entry}
	fmt.Fprintf(out, "moved %s from %s to %s\n", l.entry.Raw, l.entry.Place.Describe(), dest.Describe())
	return m.approveLine(l.file, out)
}

func sameDotenv(e config.EnvSite, arg, anchor string) bool {
	if e.Raw == arg || e.Dotenv == config.EnvSourcePathByName(arg, anchor) {
		return true
	}
	abs, err := filepath.Abs(arg)
	if err == nil && e.Dotenv == abs {
		return true
	}
	return config.SameDotenvFile(e.Dotenv, config.EnvSourcePathByName(arg, anchor))
}

// edit applies one mutation to f's file through the generic verbs' transaction, printing no
// hunk. value is "" for a removal.
func (m secretMove) edit(f config.EnvScopeFile, workspace string, action configedit.Action, path []configdoc.Step, value string, errw io.Writer) int {
	choice := config.EditTargetChoice{Kind: config.EditTargetFile, File: f.ConfigFile}
	mutation := configedit.Mutation{Action: action, Path: path}
	if action != configedit.Remove {
		mutation.Value = value
	}
	target, err := configedit.ResolveTarget(workspace, mutation, choice)
	if err != nil {
		fmt.Fprint(errw, scrubValue(configEditRefusal(err), value))
		return 1
	}
	if action != configedit.Remove {
		var ok bool
		mutation, target, ok = addMissingKey(workspace, target, mutation, value, choice, &scrubWriter{errw, value})
		if !ok {
			return 1
		}
	}
	if _, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{}); err != nil {
		fmt.Fprintf(errw, "config edit refused: %s\n", scrubValue(err.Error(), value))
		return 1
	}
	return 0
}

// approveLine is the line after a workspace config changed, as the generic verbs print it.
func (m secretMove) approveLine(f config.EnvScopeFile, out io.Writer) int {
	if f.Scope != config.EnvScopeUser {
		fmt.Fprintln(out, "The host must run `yolo config apply` to approve the workspace change.")
	}
	return 0
}

// refuseLinked refuses a dotenv file that is a symbolic link: the verb acts on the name an entry
// gives and never a link's target, and replacing the link with a file would be a guess at what
// the user meant (no symlink guessing).
func (m secretMove) refuseLinked(path string, errw io.Writer) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	fmt.Fprintf(errw, "%s is a symbolic link, and yolo config %s neither writes through a link nor replaces one; "+
		"move the line by hand in the file it points to, or make %s a regular file and run this command again\n",
		path, m.verb, path)
	return true
}

// dotenvCommand is the --dotenv form of this verb for entry, the one that moves a whole file.
func (m secretMove) dotenvCommand(entry, scope string) string {
	d := secretMove{verb: m.verb, dotenv: entry}
	return d.command(scope)
}

func (m secretMove) withDrop(scope string) string {
	d := m
	d.drop = true
	return d.command(scope)
}

func (m secretMove) inverseDrop(scope string) string {
	d := m
	d.drop = true
	if m.verb == "secret" {
		d.verb = "unsecret"
	} else {
		d.verb = "secret"
	}
	return d.command(scope)
}

func dropless(argv []string) []string {
	out := []string{"yolo", "config"}
	for _, a := range argv {
		if a != "--drop" {
			out = append(out, a)
		}
	}
	return out
}

func stepsOf(dotted string) []configdoc.Step {
	var steps []configdoc.Step
	for _, k := range strings.Split(dotted, ".") {
		steps = append(steps, configdoc.Step{Key: k})
	}
	return steps
}

func filesKey(private bool) string {
	if private {
		return "env.secrets.files"
	}
	return "env.files"
}

func privateWord(private bool) string {
	if private {
		return "private"
	}
	return "public"
}

// suggestedDotenv is the file a refusal suggests listing: beside the user config for the user
// scope, the workspace root otherwise. It is never a file the other set already lists (listing
// one in both sets puts every name in it in both), so it falls through the candidates until one
// is not.
func suggestedDotenv(f config.EnvScopeFile, private bool) string {
	candidates := []string{"defaults.env", "public.env", "defaults-public.env"}
	if private {
		candidates = []string{"secrets.env", "private.env", "secrets-private.env"}
	}
	others := f.FileEntries(!private)
	pick := candidates[len(candidates)-1]
	for _, name := range candidates {
		display := name
		if f.Scope == config.EnvScopeUser {
			display = "~/.config/yolo-jail/" + name
		}
		path := config.EnvSourcePathByName(display, f.Anchor)
		taken := false
		for _, e := range others {
			if e.Dotenv == path || config.SameDotenvFile(e.Dotenv, path) {
				taken = true
			}
		}
		if !taken {
			return display
		}
	}
	if f.Scope == config.EnvScopeUser {
		return "~/.config/yolo-jail/" + pick
	}
	return pick
}

// scrubValue replaces value in s, so no error a verb relays quotes it.
func scrubValue(s, value string) string {
	if value == "" {
		return s
	}
	return strings.ReplaceAll(s, value, "<value>")
}

// scrubPathErr keeps an os error's words, which name a path and never a value.
func scrubPathErr(err error) error {
	if pe, ok := err.(*os.PathError); ok {
		return fmt.Errorf("%s %s: %v", pe.Op, pe.Path, pe.Err)
	}
	return err
}

// scrubWriter scrubs value from everything written through it.
type scrubWriter struct {
	w     io.Writer
	value string
}

func (s *scrubWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, scrubValue(string(p), s.value)); err != nil {
		return 0, err
	}
	return len(p), nil
}
