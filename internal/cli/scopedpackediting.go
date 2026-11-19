package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func authoredPackEntries() ([]config.PackEntry, error) {
	entries, problems, err := config.LoadPackEntries()
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid packs: %v; run yolo config check", problems)
	}
	var authored []config.PackEntry
	for _, entry := range entries {
		if !entry.Implicit && !entry.ByDefault {
			authored = append(authored, entry)
		}
	}
	return authored, nil
}

func packEditReference(workspace string, value any) (config.PackEntry, error) {
	effective, err := config.LoadReadableConfig(workspace, false, nil)
	if err != nil {
		return config.PackEntry{}, err
	}
	definitions := map[string]packsrc.SourceDefinition{}
	if raw, ok := effective.Get("pack_sources"); ok {
		definitions, err = packsrc.ParseSourceDefinitions(raw)
		if err != nil {
			return config.PackEntry{}, err
		}
	}
	// A bare name remains checked against the captured shipped set by the edit
	// transaction; the parser here only computes its authored identity.
	shipped := map[string]bool{}
	if name, ok := value.(string); ok {
		shipped[strings.TrimPrefix(name, "yolo/")] = true
	}
	if obj, ok := value.(packsrc.Object); ok {
		if raw, has := obj.Get("pack"); has {
			if name, ok := raw.(string); ok {
				shipped[strings.TrimPrefix(name, "yolo/")] = true
			}
		}
	}
	ref, err := packsrc.ParsePackReference(value, definitions, filepath.Join(filepath.Dir(paths.UserConfigPath()), "packs"), paths.Home(), "pack add", shipped)
	if err != nil {
		return config.PackEntry{}, err
	}
	return config.PackEntry{Name: ref.Name, Source: ref.Source, SourceID: ref.SourceID, LocalPath: ref.LocalPath, EmbeddedName: ref.EmbeddedName, IsEmbedded: ref.EmbeddedName != "", AllowDefaultRef: ref.AllowDefaultRef, Subdir: ref.Subdir, Skills: ref.Skills, Namespace: ref.Namespace, NamespaceOwner: ref.NamespaceOwner, Landing: ref.Landing, ConfigOrigin: "pack add"}, nil
}
