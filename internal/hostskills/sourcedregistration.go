package hostskills

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/nofollow"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// RegistrationLinks returns the exact owned links under a composed destination,
// relative to that destination. Staging sync receives this plan, never permission
// inferred from a source symlink.
func RegistrationLinks(d Destination) map[string]string {
	links := map[string]string{}
	for _, l := range d.Layers {
		for _, row := range l.Registrations {
			if row.Withheld != "" || row.Outcome.State != packsrc.SkillReady || row.Plan == nil {
				continue
			}
			name := packname.Delivered(l.Namespace, row.By.SkillBaseName())
			if d.IsReserved(name) {
				continue
			}
			for _, entry := range row.Plan.Entries {
				links[filepath.Join(name, entry)] = filepath.Join(row.PayloadRoot, filepath.FromSlash(row.Plan.Path), entry)
			}
		}
	}
	return links
}

func sourcedReadinessError(dests []Destination) error {
	for _, d := range dests {
		for _, l := range d.Layers {
			for _, row := range l.Registrations {
				if row.Withheld != "" || d.IsReserved(packname.Delivered(l.Namespace, row.By.SkillBaseName())) {
					continue
				}
				if row.Outcome.State != packsrc.SkillReady || row.Plan == nil {
					return fmt.Errorf("%s", row.Outcome.Diagnostic(fmt.Sprintf("pack %s skills[%d]", l.Pack, row.Row)))
				}
			}
		}
	}
	return nil
}

func registrationContent(row packload.SourcedSkill, name string) (map[string][]byte, map[string]string) {
	files := map[string][]byte{"SKILL.md": renamedSkill(row.Plan.Marker, name)}
	if row.Plan.LicenseName != "" {
		files[row.Plan.LicenseName] = row.Plan.License
	}
	links := map[string]string{}
	for _, entry := range row.Plan.Entries {
		links[entry] = filepath.Join(row.PayloadRoot, filepath.FromSlash(row.Plan.Path), entry)
	}
	return files, links
}

func registrationChanged(dest string, files map[string][]byte, links map[string]string) bool {
	seen := map[string]bool{}
	err := nofollow.WalkBelow(filepath.Dir(dest), dest, nil, func(e nofollow.Entry) error {
		if e.Rel == "." {
			if e.Kind != nofollow.Dir {
				return fmt.Errorf("not a directory")
			}
			return nil
		}
		if filepath.Dir(e.Rel) != "." {
			return fmt.Errorf("unexpected registration descendant %s", e.Rel)
		}
		seen[e.Rel] = true
		if target, ok := links[e.Rel]; ok {
			if e.Kind != nofollow.Symlink || e.Target != target {
				return fmt.Errorf("link changed")
			}
			return nil
		}
		data, ok := files[e.Rel]
		if !ok || e.Kind != nofollow.File || e.Perm != 0644 {
			return fmt.Errorf("file changed")
		}
		got, err := io.ReadAll(io.LimitReader(e.File, int64(len(data))+1))
		if err != nil {
			return err
		}
		if !bytes.Equal(data, got) {
			return fmt.Errorf("bytes changed")
		}
		return nil
	})
	return err != nil || len(seen) != len(files)+len(links)
}

func writeSourcedRegistrations(dir string, l Layer, req ComposeRequest, preOwned map[string]bool, want map[string]string, observe bool) ([]Result, error) {
	var results []Result
	for _, row := range l.Registrations {
		if row.Withheld != "" {
			results = append(results, Result{Name: row.By.SkillBaseName(), Action: ActionReserved, Detail: row.Withheld})
			continue
		}
		name := packname.Delivered(l.Namespace, row.By.SkillBaseName())
		dest := filepath.Join(dir, name)
		if slices.Contains(l.RegistrationReserved, name) {
			results = append(results, Result{Name: name, Path: dest, Action: ActionReserved, Detail: "sourced registration withheld by the destination reservation"})
			continue
		}
		if err := packname.TooLong(name, l.Pack, "sourced skill (set as)"); err != nil {
			return results, err
		}
		if row.Outcome.State != packsrc.SkillReady || row.Plan == nil {
			results = append(results, Result{Name: name, Path: dest, Action: ActionRefused, Detail: row.Outcome.Diagnostic(fmt.Sprintf("pack %s skills[%d]", l.Pack, row.Row))})
			continue
		}
		files, links := registrationContent(row, name)
		_, err := os.Lstat(dest)
		occupied := err == nil
		if err != nil && !os.IsNotExist(err) {
			return results, err
		}
		owned := preOwned[dest] || want[dest] != ""
		if req.Composed != nil {
			_, has := req.Composed.Owner(dest)
			owned = owned || has
		}
		if occupied && !owned {
			results = append(results, Result{Name: name, Path: dest, Action: ActionSkippedUser, Detail: "confirm the adoption to migrate it into your local pack"})
			continue
		}
		result := Result{Name: name, Path: dest, Action: wroteAction(observe), Detail: "invoke as /" + name + "; whole repository is supporting data, not activation", WouldChange: true}
		if occupied && !registrationChanged(dest, files, links) {
			result.Action = ActionUnchanged
			result.WouldChange = false
		} else if !observe {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return results, err
			}
			scratch, err := os.MkdirTemp(dir, ".registration-*")
			if err != nil {
				return results, err
			}
			fail := func(err error) ([]Result, error) { _ = os.RemoveAll(scratch); return results, err }
			for entry, data := range files {
				file := filepath.Join(scratch, entry)
				if err := os.WriteFile(file, data, 0644); err != nil {
					return fail(err)
				}
				if err := os.Chmod(file, 0644); err != nil {
					return fail(err)
				}
			}
			if err := os.Chmod(scratch, 0755); err != nil {
				return fail(err)
			}
			for entry, target := range links {
				if err := os.Symlink(target, filepath.Join(scratch, entry)); err != nil {
					return fail(err)
				}
			}
			if occupied {
				if preOwned[dest] {
					if _, err := archiveComposed(req.ArchiveRoot, req.Stamp, "skills", dest, req.Composed); err != nil {
						return fail(err)
					}
				} else if err := os.RemoveAll(dest); err != nil {
					return fail(err)
				}
			}
			if err := os.Rename(scratch, dest); err != nil {
				return fail(err)
			}
		}
		want[dest] = l.Pack
		if !observe && req.Composed != nil {
			req.Composed.Record(dest, l.Pack)
			if req.Composed.RegistrationLinks == nil {
				req.Composed.RegistrationLinks = map[string]map[string]string{}
			}
			req.Composed.RegistrationLinks[dest] = links
		}
		results = append(results, result)
	}
	return results, nil
}
