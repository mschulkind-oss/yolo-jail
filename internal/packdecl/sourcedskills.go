package packdecl

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

// IsSourcedSkill distinguishes a Git registration from a pack-relative skills tree.
func (c Contribution) IsSourcedSkill() bool { return c.Kind == KindSkills && c.Source != "" }

// SkillBaseName is the declared registration name before the selecting namespace.
// Call only for a validated sourced skills row.
func (c Contribution) SkillBaseName() string {
	if c.As != "" {
		return c.As
	}
	a, _ := packsrc.Parse(c.Source)
	return path.Base(a.Path)
}

func sourcedSkillProblems(label string, c Contribution) []string {
	var problems []string
	for _, f := range []struct{ name, value string }{{"as", c.As}, {"license", c.License}} {
		if f.value != "" && !c.IsSourcedSkill() {
			problems = append(problems, fmt.Sprintf("%s: %q needs a skills row with \"source\" — add a Git source or drop %q", label, f.name, f.name))
		}
	}
	if !c.IsSourcedSkill() {
		return problems
	}
	if c.From != "" {
		problems = append(problems, label+": skills takes \"source\" or \"from\", not both — drop \"from\" for a Git registration")
	}
	if c.Agent != "" {
		problems = append(problems, label+": a skills destination takes no \"source\" — put the sourced content in a separate skills row without \"agent\"")
	}
	a, err := packsrc.Parse(c.Source)
	switch {
	case err != nil:
		problems = append(problems, label+".source: "+err.Error())
	case a.Kind != packsrc.KindGit:
		problems = append(problems, label+".source: a sourced skill needs a Git revision — for a local directory use \"from\" instead")
	default:
		u, _ := url.Parse(a.Repo)
		if u != nil && u.User != nil {
			if _, has := u.User.Password(); has {
				problems = append(problems, label+".source: a URL password is refused — remove it and use Git's credential configuration")
			}
		}
		if a.Path == "" {
			problems = append(problems, label+".source: a non-root skill subdirectory is required — add //skills/<name> before ?ref=")
		} else if c.As == "" {
			if err := packname.Word("skill subdirectory basename (or set as)", path.Base(a.Path)); err != nil {
				problems = append(problems, label+".source: "+err.Error())
			}
		}
	}
	if c.As != "" {
		if err := packname.Word("as", c.As); err != nil {
			problems = append(problems, label+".as: "+err.Error())
		}
	}
	if c.License != "" && !cleanSkillLicense(c.License) {
		problems = append(problems, label+".license: must be a clean repository-root-relative file path without '..' — write LICENSE or legal/LICENSE")
	}
	return problems
}

func cleanSkillLicense(p string) bool {
	return p != "." && p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00\r\n") && !strings.Contains("/"+p+"/", "/../")
}
