package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packstage"
)

func materializeProjectedCarrier(p *packload.Pack, entry PackEntry, dest string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	sourceRoot := p.Root
	p.SourceRoot, p.Root = sourceRoot, dest
	if p.Projection.Commit != "" {
		return nil
	}
	needsCopy := false
	for _, row := range p.Projection.Rows {
		needsCopy = needsCopy || !row.Sourced
	}
	if !needsCopy {
		return nil
	}
	carrier := filepath.Join(dest, packload.ScopedCarrierDir)
	if err := os.MkdirAll(carrier, 0755); err != nil {
		return err
	}
	for _, row := range p.Projection.Rows {
		if row.Sourced {
			continue
		}
		source := filepath.Join(sourceRoot, filepath.FromSlash(row.Path))
		marker, err := os.Lstat(filepath.Join(source, "SKILL.md"))
		state := "absent"
		if err == nil {
			if !marker.Mode().IsRegular() {
				return fmt.Errorf("selected %s/SKILL.md is not a direct regular marker; replace it with a regular file", source)
			}
			state = "present"
		} else if !os.IsNotExist(err) {
			return err
		}
		if state != row.MarkerState {
			return fmt.Errorf("selected marker changed during resolution; rerun after repairing %s", source)
		}
		target := filepath.Join(carrier, filepath.FromSlash(row.Path))
		if _, err := packstage.Stage(packstage.Spec{Root: source, Dest: target, FollowSymlinks: entry.IsLocal()}); err != nil {
			return err
		}
	}
	for _, name := range []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"} {
		info, err := os.Lstat(filepath.Join(sourceRoot, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("root license %s is unsafe or oversized; repair the license", name)
		}
		if _, err := os.Lstat(filepath.Join(carrier, name)); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sourceRoot, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(carrier, name), data, 0644); err != nil {
			return err
		}
	}
	return packload.BindProjectedLocalCarrier(p, carrier)
}
