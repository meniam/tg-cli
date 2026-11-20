package output

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"tg-cli/internal/schema"
)

// Select writes a success envelope with only the chosen paths of data.
//
// A path is written like a field of the schema: dots between the levels, no
// indexes. A list keeps one object per element with the chosen paths of it, so
// "chats.id" selects the id of every chat, and "messages.sender.name" reaches
// through the nested object. A path that the data does not have is skipped in
// silence: an optional field is absent from some responses, and asking for it
// again must not fail.
//
// Nothing new appears in the answer: a path that the data does not have adds
// nothing, and a value is copied as it is.
func Select(w io.Writer, f Format, envelope any, paths []string) error {
	return SelectWithUntrusted(w, f, envelope, paths, nil)
}

// SelectWithUntrusted gives the human renderer the marked paths after field
// selection. Machine formats ignore these paths and keep their wire output.
func SelectWithUntrusted(w io.Writer, f Format, envelope any, paths, untrusted []string) error {
	filtered, err := Filter(envelope, paths)
	if err != nil {
		return err
	}
	if f == FormatHuman {
		return HumanWithUntrusted(w, filtered, untrusted)
	}
	return Write(w, f, filtered)
}

// Filter returns the envelope with only the chosen paths of its data. It reads
// the value through its JSON form and gives back decoded JSON, so an unknown
// field of a result type cannot reach the answer.
func Filter(envelope any, paths []string) (any, error) {
	if len(paths) == 0 {
		return envelope, nil
	}
	doc, err := marshalOrdered(envelope)
	if err != nil {
		return nil, err
	}
	object, ok := doc.(obj)
	if !ok {
		return nil, fmt.Errorf("filter the response: the envelope is not an object")
	}
	data := object.get("data")
	filtered := keep(data, paths)
	out := make(obj, 0, len(object))
	for _, member := range object {
		value := member.value
		if member.key == "data" {
			value = filtered
		}
		out = append(out, field{key: member.key, value: value})
	}
	return out, nil
}

// keep returns the value with only the paths that start with one of paths. A
// list is filtered element by element; any other value is filtered as an object.
func keep(value any, paths []string) any {
	for _, path := range paths {
		if path == "" {
			return value // ItemPaths marks the whole item with an empty path
		}
	}
	switch t := value.(type) {
	case obj:
		return keepObject(t, paths)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = keep(item, paths)
		}
		return out
	}
	return value // a scalar has no paths to choose from
}

// keepObject keeps the members of an object that a path names, and a member that
// is the whole path keeps its value.
func keepObject(object obj, paths []string) obj {
	out := make(obj, 0, len(object))
	for _, member := range object {
		full := false
		var deeper []string
		for _, path := range paths {
			switch {
			case path == member.key:
				full = true
			case strings.HasPrefix(path, member.key+"."):
				deeper = append(deeper, strings.TrimPrefix(path, member.key+"."))
			}
		}
		if full {
			out = append(out, member) // the whole value, as it came
			continue
		}
		if len(deeper) == 0 {
			continue // not asked for
		}
		out = append(out, field{key: member.key, value: keep(member.value, deeper)})
	}
	return out
}

// SelectLines writes items one per line, JSONL style, each narrowed to paths,
// followed by the cursor line when more follow. The paths are relative to one
// item, because a line carries an item and not the envelope around it.
func SelectLines[T any](w io.Writer, items []T, nextCursor string, paths []string) error {
	return SelectLinesWithThrottle(w, items, nextCursor, paths, 0)
}

// SelectLinesWithThrottle appends one metadata line when a cursor or wait exists.
func SelectLinesWithThrottle[T any](w io.Writer, items []T, nextCursor string, paths []string, milliseconds int64) error {
	for _, item := range items {
		filtered, err := FilterData(item, paths)
		if err != nil {
			return err
		}
		if err := encode(w, filtered); err != nil {
			return err
		}
	}
	if nextCursor == "" && milliseconds <= 0 {
		return nil
	}
	return encode(w, struct {
		NextCursor  string `json:"next_cursor,omitempty"`
		ThrottledMS int64  `json:"throttled_ms,omitempty"`
	}{nextCursor, milliseconds})
}

// FilterData returns one value with only the chosen paths of it.
func FilterData(value any, paths []string) (any, error) {
	if len(paths) == 0 {
		return value, nil
	}
	doc, err := marshalOrdered(value)
	if err != nil {
		return nil, err
	}
	return keep(doc, paths), nil
}

// ItemPaths turns the paths of a result into the paths of one element of the
// list at itemPath, so that a JSONL line keeps the same fields as the envelope.
//
// Both "chats.id" and "id" name the id of a chat in a JSONL line.
func ItemPaths(paths []string, itemPath string) []string {
	if itemPath == "" {
		return paths
	}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		switch {
		case path == itemPath:
			out = append(out, "") // the whole item
		case strings.HasPrefix(path, itemPath+"."):
			out = append(out, strings.TrimPrefix(path, itemPath+"."))
		default:
			out = append(out, path)
		}
	}
	return out
}

// ResultPaths expands item-relative paths for a list result. A path already
// present in the result schema wins, so a result field such as next_cursor
// stays at the top level. Unknown paths stay unchanged and select nothing.
func ResultPaths(paths []string, itemPath string, fields []schema.Field) []string {
	if len(paths) == 0 || itemPath == "" {
		return paths
	}
	known := make(map[string]bool, len(fields))
	for _, field := range fields {
		known[field.Path] = true
	}
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = path
		if !known[path] && known[itemPath+"."+path] {
			out[i] = itemPath + "." + path
		}
	}
	return out
}

// ParseFields turns the value of --fields into paths.
//
// Commas separate the paths, and so does the tool know them: "a.b,c" is the two
// paths a.b and c. Spaces around a path are ignored, and so is an empty one, so
// "a, b," is a and b.
func ParseFields(value string) ([]string, error) {
	var paths []string
	for _, part := range strings.Split(value, ",") {
		path := strings.TrimSpace(part)
		if path == "" {
			continue
		}
		if err := checkPath(path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// checkPath rejects a path that cannot name a field. A path is refused rather
// than looked up blindly: a typo would otherwise select nothing in silence, and
// the agent would see an empty answer without knowing why.
func checkPath(path string) error {
	for _, part := range strings.Split(path, ".") {
		if part == "" {
			return fmt.Errorf("--fields: %q has an empty level", path)
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
				return fmt.Errorf("--fields: %q has a character that is not part of a field name", path)
			}
		}
	}
	return nil
}
