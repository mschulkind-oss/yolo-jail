package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

type packEditOptions struct {
	Handle, Git, Ref, Subdir, SkillsDir, Namespace string
	Only, Exclude                                  []string
	Bare, DryRun, HasSubdir, HasSkillsDir          bool
}

func packEditArgs(verb string, args []string, errw io.Writer) (packEditOptions, int) {
	var o packEditOptions
	fail := func(message string) (packEditOptions, int) {
		fmt.Fprintf(errw, "yolo pack %s: %s — see `yolo pack --help`\n", verb, message)
		return o, 2
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--dry-run":
			o.DryRun = true
		case "--bare":
			if verb != "add" {
				return fail("remove takes only --subdir, --skills-dir and --dry-run")
			}
			if o.Bare {
				return fail("--bare may be given once")
			}
			o.Bare = true
		case "--git", "--ref", "--subdir", "--skills-dir", "--namespace", "--only", "--exclude":
			if verb == "remove" && a != "--subdir" && a != "--skills-dir" {
				return fail("remove takes only --subdir, --skills-dir and --dry-run")
			}
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return fail(a + " needs a value")
			}
			i++
			value := args[i]
			if value == "" {
				return fail(a + " needs a non-empty value")
			}
			switch a {
			case "--git":
				if o.Git != "" {
					return fail("--git may be given once")
				}
				o.Git = value
			case "--ref":
				if o.Ref != "" {
					return fail("--ref may be given once")
				}
				o.Ref = value
			case "--subdir":
				if o.HasSubdir {
					return fail("--subdir may be given once")
				}
				o.Subdir = value
				o.HasSubdir = true
				if err := packsrc.ScopedPath(value, false); err != nil {
					return fail(err.Error())
				}
			case "--skills-dir":
				if o.HasSkillsDir {
					return fail("--skills-dir may be given once")
				}
				o.SkillsDir = value
				o.HasSkillsDir = true
				if err := packsrc.ScopedPath(value, true); err != nil {
					return fail(err.Error())
				}
			case "--namespace":
				if o.Namespace != "" {
					return fail("--namespace may be given once")
				}
				o.Namespace = value
			case "--only":
				o.Only = append(o.Only, value)
			case "--exclude":
				o.Exclude = append(o.Exclude, value)
			}
		default:
			if strings.HasPrefix(a, "-") {
				return fail("unknown flag " + a)
			}
			if o.Handle != "" {
				return fail("one pack handle at a time, got a second: " + fmt.Sprintf("%q", a))
			}
			o.Handle = a
		}
	}
	if o.Handle != "" && o.Git != "" {
		return fail("positional handle and --git are exclusive")
	}
	if o.Handle == "" && o.Git == "" {
		return fail("name the pack, for example `yolo pack " + verb + " local/matt`")
	}
	if o.Ref != "" && o.Git == "" {
		return fail("--ref requires --git")
	}
	if strings.Contains(o.Handle, "://") {
		return fail(fmt.Sprintf("%q is an address, not a handle; use `yolo pack add --git URL`", o.Handle))
	}
	if o.Only != nil || o.Exclude != nil || o.HasSkillsDir {
		if err := (packsrc.SkillSelection{Dir: o.SkillsDir, Only: o.Only, Exclude: o.Exclude}).Validate(); err != nil {
			return fail(err.Error())
		}
	}
	return o, 0
}

func (o packEditOptions) value() any {
	if o.Git == "" && !o.HasSubdir && !o.HasSkillsDir && o.Only == nil && o.Exclude == nil && !o.Bare && o.Namespace == "" {
		return o.Handle
	}
	table := jsonx.NewOrderedMap()
	if o.Git != "" {
		table.Set("git", o.Git)
		if o.Ref != "" {
			table.Set("ref", o.Ref)
		}
	} else {
		table.Set("pack", o.Handle)
	}
	if o.HasSubdir {
		table.Set("subdir", o.Subdir)
	}
	if o.HasSkillsDir || o.Only != nil || o.Exclude != nil {
		skills := jsonx.NewOrderedMap()
		if o.HasSkillsDir {
			skills.Set("dir", o.SkillsDir)
		}
		for _, list := range []struct {
			key    string
			values []string
		}{{"only", o.Only}, {"exclude", o.Exclude}} {
			if list.values != nil {
				items := make([]any, len(list.values))
				for i, s := range list.values {
					items[i] = s
				}
				skills.Set(list.key, items)
			}
		}
		table.Set("skills", skills)
	}
	if o.Bare {
		table.Set("bare", true)
	}
	if o.Namespace != "" {
		table.Set("namespace", o.Namespace)
	}
	return table
}
