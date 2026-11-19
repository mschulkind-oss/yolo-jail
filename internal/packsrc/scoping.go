package packsrc

import (
	"fmt"
	"path"
	"strings"
)

// SkillSelection is modern TOML candidate-name selection. A nil pointer means a
// whole-pack import; it is deliberately separate from legacy file filters.
type SkillSelection struct {
	Dir     string   `json:"dir,omitempty"`
	Only    []string `json:"only,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

// ScopedPath validates literal source-relative roots, never globs or pathspecs.
func ScopedPath(value string, allowDot bool) error {
	if allowDot && value == "." {
		return nil
	}
	if value == "" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") || strings.ContainsAny(value, "\\\x00") {
		return fmt.Errorf("expected a clean relative forward-slash path; omit subdir for the root")
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." || component == ".git" {
			return fmt.Errorf("path contains prohibited component %q; use a clean relative directory", component)
		}
	}
	return nil
}

// Validate preserves presence-sensitive syntax at every internal read boundary.
func (s SkillSelection) Validate() error {
	if s.Dir == "" && s.Only == nil && s.Exclude == nil {
		return fmt.Errorf("add dir, only or exclude, or remove skills for the whole pack")
	}
	if s.Dir != "" {
		if err := ScopedPath(s.Dir, true); err != nil {
			return err
		}
	}
	for _, list := range []struct {
		name   string
		values []string
	}{{"only", s.Only}, {"exclude", s.Exclude}} {
		if list.values == nil {
			continue
		}
		if len(list.values) == 0 {
			return fmt.Errorf("empty skills.%s; remove the key", list.name)
		}
		seen := map[string]bool{}
		for _, value := range list.values {
			if err := SkillSelector(value); err != nil {
				return err
			}
			if seen[value] {
				return fmt.Errorf("duplicate skills.%s selector %q; remove the duplicate", list.name, value)
			}
			seen[value] = true
		}
	}
	return nil
}

func parseScoping(obj Object, origin string) (string, *SkillSelection, error) {
	subdir := ""
	if raw, has := obj.Get("subdir"); has {
		value, ok := raw.(string)
		if !ok {
			return "", nil, fmt.Errorf("%s.subdir: expected a string", origin)
		}
		if err := ScopedPath(value, false); err != nil {
			return "", nil, fmt.Errorf("%s.subdir: %w", origin, err)
		}
		subdir = value
	}
	raw, has := obj.Get("skills")
	if !has {
		return subdir, nil, nil
	}
	selection, ok := raw.(Object)
	if !ok {
		return "", nil, fmt.Errorf("%s.skills: expected a table", origin)
	}
	if len(selection.Keys()) == 0 {
		return "", nil, fmt.Errorf("%s.skills: add dir, only or exclude, or remove skills for the whole pack", origin)
	}
	out := &SkillSelection{}
	for _, key := range selection.Keys() {
		raw, _ := selection.Get(key)
		switch key {
		case "dir":
			value, ok := raw.(string)
			if !ok {
				return "", nil, fmt.Errorf("%s.skills.dir: expected a string", origin)
			}
			if err := ScopedPath(value, true); err != nil {
				return "", nil, fmt.Errorf("%s.skills.dir: %w", origin, err)
			}
			out.Dir = value
		case "only", "exclude":
			values, ok := raw.([]any)
			if !ok {
				return "", nil, fmt.Errorf("%s.skills.%s: expected a string array", origin, key)
			}
			if len(values) == 0 {
				return "", nil, fmt.Errorf("%s.skills.%s: empty selector list; remove the key", origin, key)
			}
			names := make([]string, 0, len(values))
			seen := map[string]bool{}
			for _, raw := range values {
				value, ok := raw.(string)
				if !ok {
					return "", nil, fmt.Errorf("%s.skills.%s: expected string selectors", origin, key)
				}
				if err := SkillSelector(value); err != nil {
					return "", nil, fmt.Errorf("%s.skills.%s: %w", origin, key, err)
				}
				if seen[value] {
					return "", nil, fmt.Errorf("%s.skills.%s: duplicate selector %q; remove the duplicate", origin, key, value)
				}
				seen[value] = true
				names = append(names, value)
			}
			if key == "only" {
				out.Only = names
			} else {
				out.Exclude = names
			}
		default:
			return "", nil, fmt.Errorf("%s.skills.%s: unknown selection field; use dir, only or exclude", origin, key)
		}
	}
	return subdir, out, nil
}

// SkillSelector checks one upstream-name glob without consulting source content.
func SkillSelector(value string) error {
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return fmt.Errorf("invalid skill selector %q; use a nonempty upstream-name glob without path separators", value)
	}
	if _, err := path.Match(value, ""); err != nil {
		return fmt.Errorf("invalid skill selector %q: %w; repair the glob", value, err)
	}
	return nil
}

func scopedReference(ref PackReference, obj Object, origin string) (PackReference, error) {
	subdir, skills, err := parseScoping(obj, origin)
	if err != nil {
		return PackReference{}, err
	}
	if ref.EmbeddedName != "" && (subdir != "" || skills != nil && skills.Dir != "") {
		return PackReference{}, fmt.Errorf("%s: shipped packs allow typed skills selectors only; remove subdir and skills.dir", origin)
	}
	ref.Subdir, ref.Skills = subdir, skills
	return ref, nil
}
