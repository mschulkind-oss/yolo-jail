package macosuser

// envfile.go takes the COMPOSED LAUNCH ENVIRONMENT off this backend's argvs.
//
// # What was wrong
//
// Every process this backend starts is spelled
// `sudo --user=_yolojail /usr/bin/env -i K=V K=V … sandbox-exec … <cmd>`, and `env -i` is
// not decoration: `sudo` scrubs the environment, so the pairs cannot cross as process
// state and have to be POSITIONAL ARGUMENTS. Everything the launch composed therefore rode
// the command line — including the two things that are secrets by construction: the
// hydrated `env_sources` (the whole point of that key is to carry credentials) and the
// provider credentials the profile channel resolves into an agent's environment
// (internal/cli/run/profilechannel.go's shape vars). Measured 2026-09-12 with a fake
// dotenv: `AWS_SECRET_ACCESS_KEY` and `TAVILY_API_KEY` appeared in full on two argvs.
//
// An argv is not private. On Linux `/proc/<pid>/cmdline` is mode 0444 while
// `/proc/<pid>/environ` is 0400 — measured in this repo's own jail on 2026-09-12, where an
// unprivileged uid read a root process's full argv and was refused its environment with
// EACCES. macOS answers the same question through `sysctl kern.procargs2` instead, and
// which callers that lets read another account's argv is NOT MEASURED here (see
// docs/plans/handoff-macos-user-open-threads.md §6). The mechanism below does not depend on
// how that resolves, and the guest notch would reuse this argv shape on Linux, where the
// measurement above IS the answer.
//
// # The mechanism
//
// One per-session file, root-owned 0600, with a single `user:` ACE granting the sandbox
// account read — the SAME shape the container backends already use for the two secrets
// they carry, and no new idea: a host service's bearer token rides a 0600 endpoint file
// (internal/svcendpoint, and run.TestNoTokenInLaunchArgv asserts it is not on the podman
// argv), and the hydrated `env_sources` plus the whole profile channel ride
// `yolo-user-env.sh` at 0600 (internal/cli/run/userenv.go). This backend was the one that
// still put them on a command line.
//
// The argvs keep the identity quartet — HOME/USER/SHELL/PATH and the two store/login
// variables that travel with PATH — because those are what make the process the sandbox
// user's rather than a copy of the caller's, none of them is a secret, and a reader of the
// dry run needs them to see whose process it is. They gain one word naming the file.
//
// # Why the argv builders no longer TAKE the composed env
//
// LaunchArgv, ProvisionArgv and CaptureDriverArgv used to receive the composed
// `*jsonx.OrderedMap` and render it. They now receive the FILE PATH and never see the
// values, which is what makes "no secret is on the argv" a property of the call graph
// rather than of a loop somebody has to keep writing correctly. PlanInvariants' closed-list
// check (runplan.go) is the other half: it fails if any `K=V` outside the quartet reappears.
//
// # What this deliberately does NOT do
//
// It does not redact. `--dry-run`'s job is to print the argv that would run, a launch has
// no quiet mode by ruling (OQ-RO3), and a printed argv that differs from the real one is a
// worse tool than the exposure it hides. After this change the printed argv and the real
// argv are the same argv and neither carries a secret — so there is nothing to suppress and
// the ruling never comes up.

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// SandboxEnvFileEnv names the session env file on the argv. The value is a PATH, so the
// word itself is safe to print, log and tee into <workspace>/.yolo/launch.log.
//
// It has two consumers. On the sandboxed argvs, the `sh -c` reader ExecWithEnvFile wraps
// each one in sources the file it is handed as an argument, and the variable is emitted
// beside it so a human reading `ps` or a dry run can find the file that explains the
// environment they cannot see on the command line. On the BOOTSTRAP argv the variable is the
// whole delivery: the bootstrap reads the file into its generator Env (entrypoint's
// hydrate_session_env step), never into its process environment, so the requires_env gate
// it renders every agent's MCP table through sees what the agent will have.
//
// Spelled once, by the reader (entrypoint.DarwinSessionEnvFileEnv), since macosuser imports
// entrypoint and not the reverse.
const SandboxEnvFileEnv = entrypoint.DarwinSessionEnvFileEnv

// sandboxEnvLeaf is the state-dir subdir holding each session's env file. A subdir of its
// own, not a sibling of the staged binary, because the directory carries a 0700 mode and a
// search ACE that the rest of the state dir must not have: /var/yolo-jail itself holds the
// staged yolo and the pack trees, which are `a+rX` on purpose.
const sandboxEnvLeaf = "env"

// sandboxFileReadRights is the ACE right-set a file the sandbox must READ needs, and
// nothing more. Read, never write: a file the sandbox could rewrite is a sandbox that
// chooses its own environment, which is the same rule the staged yolo binary is under
// (PlanInvariants' root-owned-state-dir check).
const sandboxFileReadRights = "read,readattr,readextattr,readsecurity"

// SandboxEnvFile is the per-session env file: <stateDir>/env/<cname>.env.
//
// Per SESSION rather than per workspace, keyed the same way the Seatbelt profile and the
// staged pack tree are (cnameFor the workspace), so two workspaces launching at once cannot
// read each other's composed environment out of one file.
func SandboxEnvFile(cname, sd string) string {
	if sd == "" {
		sd = stateDir
	}
	if cname == "" {
		return ""
	}
	return sd + "/" + sandboxEnvLeaf + "/" + cname + ".env"
}

// SandboxEnvFileContent renders the composed launch env as shell `export K='v'` lines.
//
// The grammar is exportPlain's, from the container's yolo-user-env.sh (userenv.go): single
// quotes, with an embedded quote closed-escaped-reopened. One grammar for both backends
// means a value that survives one crossing survives the other.
//
// Keys are emitted in the caller's order, not sorted, because the composed env is already
// ordered by the layering that produced it (pack env, then providers, then env_sources,
// then the caller's own) and re-sorting would make a later layer's override land first.
//
// The PROTECTED QUARTET IS FILTERED HERE TOO, not only on the argv. The file is sourced
// after `env -i` has set them, so a composed HOME or PATH in it would override the identity
// this backend assigns — the very thing sandboxEnvPairs drops them to prevent. Filtering in
// one place would leave the other half of the pair enforcing nothing.
func SandboxEnvFileContent(sandboxEnv *jsonx.OrderedMap) string {
	if sandboxEnv == nil {
		return ""
	}
	protected := protectedSandboxEnvKeys()
	var b strings.Builder
	for _, k := range sandboxEnv.Keys() {
		if _, ok := protected[k]; ok {
			continue
		}
		v, _ := sandboxEnv.Get(k)
		b.WriteString(exportLine(k, asStr(v)))
	}
	return b.String()
}

// exportLine renders one `export K='v'` line. Factored out of the renderer so the
// invariant that asks "did this exact value reach the file?" can ask through the SAME
// escaping rather than through a second copy of it — a check that quoted differently
// from the writer would pass or fail on the quoting instead of on the value.
func exportLine(key, value string) string {
	return "export " + key + "='" + strings.ReplaceAll(value, "'", `'\''`) + "'\n"
}

// SandboxEnvFileSets reports whether a rendered env file exports key with exactly value.
//
// Rendered bytes rather than the source map, for SandboxEnvFileKeys' reason: the question
// is what the sandbox will READ, and a second pass over the map could answer it
// differently from what was written.
func SandboxEnvFileSets(content, key, value string) bool {
	return strings.Contains(content, exportLine(key, value))
}

// sandboxEnvFileValue returns the value a rendered env file exports for key, undoing
// exportLine's escaping, and whether the file sets key at all. The last line wins, as it
// does when the shell sources the file.
func sandboxEnvFileValue(content, key string) (string, bool) {
	prefix := "export " + key + "='"
	value, found := "", false
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(line, prefix)
		if !ok {
			continue
		}
		rest, ok = strings.CutSuffix(rest, "'")
		if !ok {
			continue
		}
		value, found = strings.ReplaceAll(rest, `'\''`, "'"), true
	}
	return value, found
}

// SandboxEnvFileKeys returns the variable names a rendered env file sets, in file order.
//
// It reads the RENDERED BYTES rather than taking the map again, because its caller is the
// plan invariant that asks "did any of these end up on an argv?" — a question about what
// was written, which a second pass over the source map could answer differently.
func SandboxEnvFileKeys(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		rest, ok := strings.CutPrefix(line, "export ")
		if !ok {
			continue
		}
		if k, _, ok := strings.Cut(rest, "="); ok && k != "" {
			out = append(out, k)
		}
	}
	return out
}

// SandboxEnvDirCommands prepare the env file's directory: root-owned, 0700, with a SEARCH
// ace for the sandbox account.
//
// THEY MUST RUN BEFORE THE FILE IS WRITTEN, and that ordering is the whole reason they are
// a separate list from SandboxEnvGrantCommands. InstallRootFile writes through `sudo tee`,
// which creates the file 0644 and narrows it a command later; in a 0700 directory that
// window is unreachable by anyone, while in the state dir's own 0755 it is a world-readable
// copy of every credential for as long as two subprocesses take to start.
//
// 0700 plus a search ACE, not 0755: search is traverse-not-list, so the sandbox can open
// the file it is told about and cannot enumerate other sessions' files beside it.
func SandboxEnvDirCommands(envFile, user string) [][]string {
	if envFile == "" {
		return nil
	}
	if user == "" {
		user = SandboxUser
	}
	dir := pathParent(envFile)
	return [][]string{
		{mkdirBin, "-p", dir},
		{chmodBin, "0700", dir},
		{chmodBin, "+a", "user:" + user + " allow search", dir},
	}
}

// SandboxEnvGrantCommands grant the sandbox account read on the written file. They run
// AFTER the write, because `sudo tee` replaces the file and an ACE set beforehand would be
// set on a file that no longer exists.
func SandboxEnvGrantCommands(envFile, user string) [][]string {
	if envFile == "" {
		return nil
	}
	return [][]string{sandboxFileReadAce(envFile, user)}
}

// SandboxEnvRemoveCommands delete the file. Best-effort at every call site: a session whose
// agent exited must not be reported as failed because its env file could not be swept, and
// the next launch of the same workspace overwrites the same path.
func SandboxEnvRemoveCommands(envFile string) [][]string {
	if envFile == "" {
		return nil
	}
	return [][]string{{rmBin, "-f", envFile}}
}

// sandboxFileReadAce is the one `chmod +a` that lets the sandbox uid read one root-owned
// file. Shared by the env file and by EndpointGrantCommands, which is the same grant on a
// host service's endpoint — one spelling, so the two cannot drift into different right-sets.
//
// A `user:` ACE, not a `group:` one: SandboxGroup contains the host user
// (SharedRootProvisionCommands adds them), so a group ACE would widen the grant past the
// single account that needs it.
//
// NOT EXECUTABLE ON LINUX — `chmod +a` is a macOS ACL extension. The argv is built and
// unit-tested here; only a Mac can run it.
func sandboxFileReadAce(path, user string) []string {
	if user == "" {
		user = SandboxUser
	}
	return []string{chmodBin, "+a", "user:" + user + " allow " + sandboxFileReadRights, path}
}

// sandboxEnvReader is the shell body every sandboxed argv is wrapped in: source the file
// named by $1, then exec the rest.
//
// THE REAL ARGV IS NOT RE-QUOTED. `$@` after the shift is the wrapped command word for
// word, so a wrapper that reads the environment costs nothing in quoting risk and the
// argv a dry run prints is the argv `execve` gets. That matters here specifically because
// the launch and provisioning argvs deliberately carry no `--login` (see LaunchArgv and
// ProvisionArgv): sudo's login path CONCATENATES and re-escapes, and a second quoting
// layer is exactly what this repo already measured going wrong.
//
// `exec` replaces the shell, so nothing extra survives in the process tree — the agent
// keeps the pid and the terminal the TTY proxy gave it.
//
// `|| exit 1` FAILS CLOSED. An unreadable env file means the agent would run with no
// credentials and no provider configuration; starting anyway produces an agent that
// authenticates against nothing and reports a confusing failure minutes later.
const sandboxEnvReader = `. "$1" || exit 1; shift; exec "$@"`

// sandboxEnvReaderName is $0 for the reader — it is what shows in a `ps` line and in a
// dry run, so it says what the shell is for rather than reading as a stray `sh`.
const sandboxEnvReaderName = "yolo-sandbox-env"

// sandboxEnvShell is the reader's interpreter. /bin/sh, spelled absolutely like every other
// binary this package invokes, and deliberately NOT the shell the wrapped command uses: the
// wrapper's only job is `.` and `exec`, which is POSIX, while the launch wants zsh and the
// provisioning stage wants bash for their own bodies.
const sandboxEnvShell = "/bin/sh"

// ExecWithEnvFile wraps an argv so the composed environment is read from the session env
// file instead of the command line. An empty envFile returns argv unchanged, which is what
// a plan that composed nothing produces.
func ExecWithEnvFile(envFile string, argv []string) []string {
	if envFile == "" || len(argv) == 0 {
		return argv
	}
	out := []string{sandboxEnvShell, "-c", sandboxEnvReader, sandboxEnvReaderName, envFile}
	return append(out, argv...)
}

// protectedSandboxEnvKeys is the closed set of variables THIS BACKEND owns: the identity
// quartet, the mise store that travels with PATH, and the login-shell PATH copy. A caller
// may not set any of them, on the argv or through the env file, because they are what make
// the process the sandbox user's rather than a copy of whatever the invoking shell had.
//
// It is also the allowlist the plan invariant checks the argvs against, which is why it is
// a function returning a fresh map rather than a package-level var: a check that mutated
// the set it shares with the renderer would be a check that changes what it measures.
func protectedSandboxEnvKeys() map[string]struct{} {
	out := map[string]struct{}{}
	for _, k := range ProtectedSandboxEnvNames() {
		out[k] = struct{}{}
	}
	return out
}

// ProtectedSandboxEnvNames is protectedSandboxEnvKeys as a sorted list, for messages and
// for tests that want to name the set rather than restate it.
func ProtectedSandboxEnvNames() []string {
	out := []string{"HOME", "USER", "SHELL", "PATH", "MISE_DATA_DIR", entrypoint.DarwinLoginPathEnv}
	sort.Strings(out)
	return out
}

// installSandboxEnvFile writes one session env file and grants the sandbox account read on
// it, in the only order that has no window: prepare the 0700 directory, write through
// `sudo tee` (InstallRootFile — content on STDIN, never argv, the same rule
// setRandomPasswordReal follows for the account password), then add the read ACE.
//
// IT FAILS CLOSED and its callers abort. The `sh -c` reader on each argv also fails closed,
// so a launch that continued here would die a step later with a shell error naming a path;
// stopping at the write names what actually went wrong. A plan that composed nothing has no
// file, and this returns true having done nothing.
//
// Shared by the launch (RunMacosUser) and the install capture (RunCapturePlan) because the
// capture's whole contract is to resemble a launch — a second spelling of the delivery would
// be a second way for the two to diverge.
func installSandboxEnvFile(deps Deps, out printer, plan sandboxEnvPlan) bool {
	envFile, content := plan.envFile()
	if envFile == "" {
		return true
	}
	dirCmds, grantCmds := plan.envFileCommands()
	for _, cmd := range dirCmds {
		if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
			out.printf("[bold red]Could not prepare the session environment directory "+
				"(%s).[/bold red]", strings.Join(cmd, " "))
			return false
		}
	}
	if !deps.InstallRootFile(envFile, content, "0600") {
		out.printf("[bold red]Could not write the session environment file %s.[/bold red]\n"+
			"It carries everything this launch composed — the profile/provider channel and "+
			"the hydrated env_sources — so the sandbox would start with none of it.", envFile)
		return false
	}
	for _, cmd := range grantCmds {
		if deps.Run(append([]string{"sudo"}, cmd...)) != 0 {
			out.printf("[bold red]Could not grant %s read on %s (%s).[/bold red]",
				SandboxUser, envFile, strings.Join(cmd, " "))
			return false
		}
	}
	return true
}

// sandboxEnvPlan is the slice of a plan installSandboxEnvFile needs. RunPlan and
// CapturePlan both satisfy it, which is what lets one installer serve both without either
// struct learning about the other.
type sandboxEnvPlan interface {
	envFile() (path, content string)
	envFileCommands() (dir, grant [][]string)
}

// SandboxArgvEnvProblems is the invariant that keeps a secret off a sandboxed argv, and it
// is written as a CLOSED ALLOWLIST rather than as a search for known-bad names.
//
// A denylist of "credential-looking" keys is the shape that fails: it has to be kept in step
// with every provider a pack may add and with every variable a user's dotenv may hold, and
// the first one nobody thought of ships silently. The allowlist is the six identity
// variables this backend owns plus the two words that describe the delivery — every one of
// them a fact about the sandbox, none of them composed from user input — so a NEW composed
// variable fails this check by existing.
//
// It reads the argv the way `env` does: everything from `-i` up to the first word that is
// not `K=V` is the environment, and that word is the command. So it measures the real
// environment rather than a prefix somebody has to keep counting.
//
// ⚠ IT DELIBERATELY DOES NOT COVER THE BOOTSTRAP ARGV. That argv carries the generator
// contract — the wire tables the darwin bootstrap reads — and those variables are its
// command-line interface, not composed user values; see docs/plans/handoff-macos-user-open-threads.md §6
// for what that argv does and does not hold and why the one residual there is a
// cross-backend question rather than this backend's.
func SandboxArgvEnvProblems(label string, argv []string) []string {
	allowed := protectedSandboxEnvKeys()
	allowed[SandboxEnvFileEnv] = struct{}{}
	// The provisioning stage's one extra pair — a fact about the stage (it must outrun the
	// blocked-tool shims), with a literal value no user composes.
	allowed["YOLO_BYPASS_SHIMS"] = struct{}{}

	var problems []string
	inEnv := false
	for _, word := range argv {
		if !inEnv {
			inEnv = word == "-i"
			continue
		}
		key, _, isPair := strings.Cut(word, "=")
		if !isPair || key == "" {
			break // `env` stops here too: this word is the command.
		}
		if _, ok := allowed[key]; !ok {
			problems = append(problems, "the "+label+" argv carries the composed variable "+
				key+" as a command-line word; every composed value crosses in the session "+
				"env file instead, because an argv is world-readable and a process "+
				"environment is not")
		}
	}
	return problems
}

// SandboxArgvReadsEnvFile reports whether an argv actually reads the session env file.
//
// The other half of the pair above, and the half that fails when a CALL SITE is deleted:
// dropping ExecWithEnvFile from one argv builder leaves that argv clean of secrets and
// silently short of every variable the launch composed — an agent with no credentials, and
// a check that only looked for leaks would stay green.
func SandboxArgvReadsEnvFile(envFile string, argv []string) bool {
	if envFile == "" {
		return true
	}
	sawReader, sawFile := false, false
	for _, word := range argv {
		switch word {
		case sandboxEnvReader:
			sawReader = true
		case envFile:
			sawFile = true
		}
	}
	return sawReader && sawFile
}
