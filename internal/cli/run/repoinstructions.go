package run

// repoinstructions.go is the launch's half of `repo_instructions`
// (docs/reference/workspace-instruction-files.md): resolve the mode once above the dispatch, hand it to
// every staged pack, and say what it did. The rendering is the packs' (their `instructions`
// declaration, through the surface fold and the launch-flag fold) and the replacement text joins the
// briefing through the user block (prepare.go); this file owns neither.
//
// THERE IS NO MASK (IF-D14): nothing here adds a mount, and an agent that cannot ignore the files is
// named in a line that never refuses the launch (IF-D15).

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/repoinstr"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// applyRepoInstructions resolves the launch's repo_instructions and hands the selection to the
// staged packs, so the argv rewrite above the dispatch carries each agent's flags. It reports
// false after printing a refusal: a replace_with that cannot be read stops the launch, naming the
// path and the command that removes it. A sealed build (a fork's capture) applies nothing: it
// carries none of the user's own text.
func (o *Options) applyRepoInstructions(cfg *jsonx.OrderedMap, rt string, packs []*packload.Pack) bool {
	if o.Sealed {
		o.repoInstr = &repoinstr.Resolved{Mode: "keep"}
		return true
	}
	res, err := repoinstr.Resolve(cfg, o.Workspace)
	if err != nil {
		o.pr(o.Stderr).printf("[bold red]Refusing to launch: %s[/bold red]", richtext.Escape(err.Error()))
		return false
	}
	o.repoInstr = res
	workspace, home := jailWorkspace, "/home/agent"
	if rt == "macos-user" { // parity: HonoredBy — the guest sees the real workspace path and the sandbox account's home, so the same selection names them; the guest boot rebuilds it from the same session input
		workspace, home = resolvePath(o.Workspace), macosuser.SandboxHome()
	}
	packload.ApplyInstructions(packs, res.Selection(workspace, home, false))
	return true
}

// discloseRepoInstructions prints, per selected agent, what the mode did to the files it reads, or
// that it did nothing because the agent has no setting for it. NOT SUPPRESSIBLE: a launch has no quiet
// mode by ruling (OQ-RO3), YOLO_NO_BANNER covers the version line and nothing else, and every line is
// teed to launch.log with the rest. Silent for a launch that keeps the files, which is most of them.
func (o *Options) discloseRepoInstructions(packs []*packload.Pack) {
	if !o.repoInstr.Ignoring() {
		return
	}
	out := o.pr(o.Stderr)
	for _, line := range repoinstr.Disclosure(packs, packload.Embedded(), o.repoInstr, resolvePath(o.Workspace), false, "") {
		out.print("[yellow]" + richtext.Escape(line) + "[/yellow]")
	}
}
