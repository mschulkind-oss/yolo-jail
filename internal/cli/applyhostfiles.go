package cli

// applyhostfiles.go is the `yolo host apply` call site for the `files` kind: a pack's owned
// tree, written into the real home instead of bind-mounted into a jail.
//
// It shares the ownership record with skill delivery (hostSkillsManifestPath) rather than
// keeping a second one. Both answer the same question about the same home — "did yolo write
// this path?" — and two records could disagree, at which point neither is evidence. The
// record is keyed by absolute destination, so a skills entry and a files entry cannot
// collide in it.

import (
	"io"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// applyHostFiles writes one pack's `files` contributions into the real home and prints one
// line per file. write=false is the observe posture.
//
// The manifest is loaded and saved HERE rather than threaded from the caller because a
// corrupt or missing record must fail closed independently: a files render that cannot prove
// ownership refuses and touches nothing, which is the correct outcome whether or not skill
// delivery in the same run had the same problem.
func applyHostFiles(pr richtext.Printer, errw io.Writer, p *packload.Pack, home, stamp string,
	write bool, survey *hostApplySurvey) int {
	if !declaresKind(p, packdecl.KindFiles) {
		return 0
	}
	manPath := hostSkillsManifestPath()
	man, err := hostskills.LoadManifest(manPath)
	if err != nil {
		pr.Printf("  [yellow]⚠ files: %v — treating every existing path as yours[/yellow]", err)
	}

	results, rerr := entrypoint.RenderHostFiles(p, home, entrypoint.HostFilesRequest{
		Manifest: man,
		// The kind's OWN bucket (V3). It shared `archive/skills` with every other host kind,
		// so a replaced `files` copy landed under a directory named for skills — the one place
		// a user looking for it will not look. The ownership RECORD is still shared with skill
		// delivery (see the file header); the archive is not, because they answer different
		// questions: one is keyed by path and must not fork, the other is a place a human
		// browses and must say what it holds.
		ArchiveRoot: hostArchiveRoot(string(packdecl.KindFiles)),
		Stamp:       stamp,
	}, !write)
	if rerr != nil {
		pr.Printf("  [red]files      failed[/red] — %v", rerr)
		return 1
	}
	for _, r := range results {
		survey.note(string(packdecl.KindFiles), r.Surface, r.Path, r.WouldChange)
		pr.Printf("  [cyan]%-20s[/cyan] %s  [dim]%s[/dim]", r.Surface, r.Action, r.Path)
	}
	if write {
		if err := man.Save(manPath); err != nil {
			pr.Printf("  [yellow]⚠ files: could not save the ownership record: %v[/yellow]", err)
			pr.Printf("  [dim]  (the files were written; the next apply will treat them as " +
				"yours and leave them alone)[/dim]")
		}
	}
	return 0
}

// declaresKind reports whether the pack declares at least one contribution of this kind.
func declaresKind(p *packload.Pack, kind packdecl.Kind) bool {
	for _, c := range p.Decl.Contributions() {
		if c.Kind == kind {
			return true
		}
	}
	return false
}
