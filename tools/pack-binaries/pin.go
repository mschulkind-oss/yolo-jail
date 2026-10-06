package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// buildPin is what `pin` writes for one build: its sha256, and its url unless URL is "", which
// keeps the url the manifest has (the digest-only pin main makes between releases, BP-D15).
type buildPin struct {
	Binary, Platform, URL, SHA256 string
}

// pinManifest writes each pin's url and sha256 into data IN PLACE: it replaces the bytes of
// those two string values and nothing else, so every comment and every other byte of the file
// survives. Each value must already be there as a string — `pin` writes values, never keys, so
// a build is added or removed by the manifest's author (the census names which).
func pinManifest(data []byte, pins []buildPin) ([]byte, error) {
	type edit struct {
		span json5.Span
		text string
	}
	var edits []edit
	for _, p := range pins {
		fields := []struct{ key, value string }{{"sha256", p.SHA256}}
		if p.URL != "" {
			fields = append(fields, struct{ key, value string }{"url", p.URL})
		}
		for _, f := range fields {
			path := []string{"binaries", p.Binary, p.Platform, f.key}
			span, ok, err := json5.Locate(data, path...)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("the manifest has no %s", strings.Join(path, "."))
			}
			if q := data[span.Start]; q != '"' && q != '\'' {
				return nil, fmt.Errorf("%s is not a string", strings.Join(path, "."))
			}
			if strings.ContainsAny(f.value, "\"'\\\n\r\t") {
				return nil, fmt.Errorf("refusing to write %q into %s", f.value, strings.Join(path, "."))
			}
			edits = append(edits, edit{span: span, text: `"` + f.value + `"`})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].span.Start > edits[j].span.Start })
	out := append([]byte(nil), data...)
	for i, e := range edits {
		if i > 0 && e.span.End > edits[i-1].span.Start {
			return nil, fmt.Errorf("two values overlap at byte %d", e.span.Start)
		}
		out = append(out[:e.span.Start], append([]byte(e.text), out[e.span.End:]...)...)
	}
	return out, nil
}

// writePins pins the manifest at root/rel and reads it back through the strict decoder,
// refusing unless every build decodes to what was written. It returns whether the file changed.
func writePins(root, rel string, pins []buildPin) (bool, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	out, err := pinManifest(data, pins)
	if err != nil {
		return false, fmt.Errorf("%s: %w", rel, err)
	}
	dir := filepath.Dir(rel)
	m, err := loopholedecl.Decode(out, dir)
	if err != nil {
		return false, fmt.Errorf("%s: the pinned manifest no longer decodes: %w", rel, err)
	}
	for _, p := range pins {
		var got loopholedecl.BinaryBuild
		found := false
		for _, b := range m.Binaries {
			if b.Name == p.Binary {
				got, found = b.BuildFor(p.Platform)
			}
		}
		if !found || (p.URL != "" && got.URL != p.URL) || got.SHA256 != p.SHA256 {
			return false, fmt.Errorf("%s: after pinning, %s %s reads back as %+v, not %s %s",
				rel, p.Binary, p.Platform, got, p.URL, p.SHA256)
		}
	}
	if string(out) == string(data) {
		return false, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pin-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(fi.Mode().Perm()); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}
