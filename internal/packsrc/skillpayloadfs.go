package packsrc

import (
	"fmt"
	"io/fs"
)

// ReadEmbeddedSkillPayloadTree validates immutable shipped bytes without making
// a filesystem tree. The embedded source admits only directories and regular files.
func ReadEmbeddedSkillPayloadTree(source fs.FS, rows []SkillRequest) (*SkillPayloadPlan, error) {
	plan := &SkillPayloadPlan{}
	err := fs.WalkDir(source, ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		entry := SkillEntry{Path: path}
		plan.ChargedEntries++
		if plan.ChargedEntries > skillEntryLimit {
			return fmt.Errorf("embedded payload entries exceed bound %d", skillEntryLimit)
		}
		if info.IsDir() {
			entry.Mode = "040000"
		} else if info.Mode().IsRegular() {
			entry.Mode = "100644"
			if info.Mode()&0111 != 0 {
				entry.Mode = "100755"
			}
			entry.Size = info.Size()
			if plan.ChargedBytes+entry.Size > skillByteLimit {
				return fmt.Errorf("embedded payload bytes exceed bound %d", skillByteLimit)
			}
			entry.Data, err = fs.ReadFile(source, path)
			if err != nil {
				return err
			}
			if int64(len(entry.Data)) != entry.Size {
				return fmt.Errorf("embedded file changed while reading %s", path)
			}
		} else {
			return fmt.Errorf("embedded payload %s is not a regular file or directory", path)
		}
		plan.ChargedBytes += entry.Size
		plan.Entries = append(plan.Entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return validateSkillPayloadPlan(plan, rows)
}
