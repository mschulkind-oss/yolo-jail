package run

// termProgramVar names the terminal emulator the user is typing in, as the terminal itself
// sets it (Ghostty `ghostty`, herdr `herdr`, iTerm2 `iTerm.app`, WezTerm `WezTerm`, VS Code
// `vscode`, …). TERM says which escape sequences a terminal speaks; TERM_PROGRAM says which
// terminal it is, and agent TUIs gate features TERM cannot express on it. pi-tui turns OSC 8
// hyperlinks on only for a terminal it positively identifies, and under herdr (TERM is
// `xterm-256color`) or any terminal whose TERM is a generic xterm, TERM_PROGRAM is the only
// signal that reaches it — without it every link in pi's output renders as `text (url)`.
//
// It names a program, never a path or a credential, so it crosses like TERM and COLORTERM.
// HERDR_ENV and the other HERDR_* handles deliberately do not: they promise a socket and a
// binary the jail does not have (docs/research/herdr-integration.md).
const termProgramVar = "TERM_PROGRAM"

// termProgramEnvArgs is the `-e TERM_PROGRAM=<value>` pair that carries this invocation's
// terminal into the container, or nothing when it is unset or empty.
//
// ONE builder for BOTH argvs that start a process in the jail: assembleRunCmd's fresh launch
// and attachExisting's `exec` (through attachTermProgramEnvArgs).
func (o *Options) termProgramEnvArgs() []string {
	if v := o.Getenv(termProgramVar); v != "" {
		return []string{"-e", termProgramVar + "=" + v}
	}
	return nil
}

// attachTermProgramEnvArgs is the attach exec's TERM_PROGRAM. The terminal is the
// ATTACHING one's, not the launch's: a jail launched from Ghostty and attached from a
// terminal that swallows OSC 8 must not tell its agents they are in Ghostty, or their links
// would render as invisible text. So this invocation's value rides the exec, and a value
// the launch froze into the container is cleared, EMPTY, when this invocation has none —
// `podman exec` cannot remove a variable, and an empty TERM_PROGRAM names no terminal
// (the attachNoColorEnvArgs shape).
//
// The empty pair goes only to a container whose inspect SHOWED a non-empty value; an
// inspect that answered nothing proves nothing, so it changes nothing.
func (o *Options) attachTermProgramEnvArgs(envLines []string) []string {
	if args := o.termProgramEnvArgs(); args != nil {
		return args
	}
	if envLineValue(envLines, termProgramVar) != "" {
		return []string{"-e", termProgramVar + "="}
	}
	return nil
}
