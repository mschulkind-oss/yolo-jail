package packdecl

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EncodeLoopholeTOMLLines writes a legacy loophole manifest.jsonc tree (configdoc.Parse's
// Values, with its authored key order) as loophole.toml lines, each carrying the legacy path it
// came from so a migration can put every comment back in order (docs/reference/config/rulings.md
// RC-D28).
//
// The spellings are the schema's own keys, unchanged. The layout is: every plain key of a table
// first (TOML cannot return to a table's plain keys after a sub-table header), then its
// sub-tables in written order. A table whose members are all plain and fit one line stays an
// inline value of its parent; an array of tables that does not fit one line is written as
// `[[array of tables]]` blocks. A JSON null has no TOML spelling and is refused naming its path
// (UnrepresentableError), never guessed.
//
// Every key is carried as written, with its value. A retired key (`default_enabled`) is not
// dropped here: the strict decode the migration proves against refuses it, naming what replaces it.
func EncodeLoopholeTOMLLines(tree *jsonx.OrderedMap) ([]TOMLLine, error) {
	if tree == nil {
		return nil, fmt.Errorf("packdecl: cannot encode a nil loophole manifest")
	}
	return encodeLoopholeTable(tree, nil, nil)
}

// loopholeInlineTable reports whether a table stays inline in its parent: every member a plain
// value, and the whole `key = { … }` within one line.
func loopholeInlineTable(key string, table *jsonx.OrderedMap) bool {
	if table.Len() == 0 {
		return true
	}
	for _, member := range table.Keys() {
		item, _ := table.Get(member)
		if _, isTable := item.(*jsonx.OrderedMap); isTable {
			return false
		}
		if list, isList := item.([]any); isList && len(loopholeTablesOf(list)) != 0 {
			return false
		}
	}
	inline, err := tomlInline(table)
	return err == nil && len(key)+3+len(inline) <= tomlLineWidth
}

// loopholeTablesOf is the list's items when every one is a table, else nil.
func loopholeTablesOf(list []any) []*jsonx.OrderedMap {
	if len(list) == 0 {
		return nil
	}
	out := make([]*jsonx.OrderedMap, 0, len(list))
	for _, item := range list {
		table, ok := item.(*jsonx.OrderedMap)
		if !ok {
			return nil
		}
		out = append(out, table)
	}
	return out
}

// encodeLoopholeTable writes one table: its plain keys first, then its sub-tables.
func encodeLoopholeTable(table *jsonx.OrderedMap, header []string, from []configdoc.Step) ([]TOMLLine, error) {
	plain, children, err := encodeLoopholeParts(table, header, from)
	if err != nil {
		return nil, err
	}
	return append(plain, children...), nil
}

// encodeLoopholeParts is encodeLoopholeTable's two halves, kept apart so a table whose members are
// all sub-tables can write no header of its own: its first sub-table's header creates it.
func encodeLoopholeParts(table *jsonx.OrderedMap, header []string, from []configdoc.Step) (plain, children []TOMLLine, err error) {
	for _, key := range table.Keys() {
		value, _ := table.Get(key)
		path := appendStep(from, configdoc.Step{Key: key})
		keyText, err := tomlKeyText(key)
		if err != nil {
			return nil, nil, err
		}
		childHeader := append(append([]string(nil), header...), keyText)
		switch v := value.(type) {
		case *jsonx.OrderedMap:
			if loopholeInlineTable(key, v) {
				emitted, err := encodeMember(key, v, path, false)
				if err != nil {
					return nil, nil, err
				}
				plain = append(plain, emitted...)
				continue
			}
			subPlain, subChildren, err := encodeLoopholeParts(v, childHeader, path)
			if err != nil {
				return nil, nil, err
			}
			if len(subPlain) != 0 || len(subChildren) == 0 {
				children = append(children, TOMLLine{}, TOMLLine{Text: "[" + strings.Join(childHeader, ".") + "]", Source: path})
			}
			children = append(children, subPlain...)
			children = append(children, subChildren...)
			continue
		case []any:
			if items := loopholeTablesOf(v); items != nil {
				inline, err := tomlInline(v)
				if err == nil && len(keyText)+3+len(inline) <= tomlLineWidth && !loopholeHasNestedTable(items) {
					emitted, err := encodeMember(key, v, path, false)
					if err != nil {
						return nil, nil, err
					}
					plain = append(plain, emitted...)
					continue
				}
				for i, item := range items {
					itemPath := appendStep(path, configdoc.Step{Index: i, IsIndex: true})
					children = append(children, TOMLLine{}, TOMLLine{Text: "[[" + strings.Join(childHeader, ".") + "]]", Source: itemPath})
					body, err := encodeLoopholeTable(item, childHeader, itemPath)
					if err != nil {
						return nil, nil, err
					}
					children = append(children, body...)
				}
				continue
			}
		}
		emitted, err := encodeMember(key, value, path, false)
		if err != nil {
			return nil, nil, err
		}
		plain = append(plain, emitted...)
	}
	return plain, children, nil
}

func loopholeHasNestedTable(items []*jsonx.OrderedMap) bool {
	for _, item := range items {
		for _, key := range item.Keys() {
			value, _ := item.Get(key)
			if _, ok := value.(*jsonx.OrderedMap); ok {
				return true
			}
		}
	}
	return false
}
