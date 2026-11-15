package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
)

// Paths lists the paths that actually appear in a JSON document, using the same
// notation as Fields: dots between the levels, no indexes. It is the other side
// of Fields: a path of a real response must be one of the schema paths, and this
// is what a test compares.
//
// A key that json encodes with a different name, like an empty or strange one,
// is reported as it appears; the caller compares the two lists and reports the
// difference.
func Paths(doc []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	set := map[string]bool{}
	if err := walkJSON(dec, "", set); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("more than one JSON value")
	}
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// walkJSON reads one JSON value and adds the paths of its keys.
func walkJSON(dec *json.Decoder, prefix string, set map[string]bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil // a scalar has no paths
	}
	switch delim {
	case '{':
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := keyTok.(string)
			path := join(prefix, key)
			set[path] = true
			if err := walkJSON(dec, path, set); err != nil {
				return err
			}
		}
		_, err = dec.Token() // the closing }
		return err
	case '[':
		for dec.More() {
			if err := walkJSON(dec, prefix, set); err != nil {
				return err
			}
		}
		_, err = dec.Token() // the closing ]
		return err
	}
	return nil
}

// Missing returns the paths of a response that the schema does not describe.
//
// Members the schema cannot know are not asked for: the keys of a map, the
// members of a raw JSON value, and the levels of a type that contains itself
// below the first one.
func Missing(actual []string, fields []Field) []string {
	known := make(map[string]bool, len(fields))
	for _, f := range fields {
		known[f.Path] = true
	}
	var missing []string
	for _, p := range actual {
		if !known[p] && !opaque(p, fields) {
			missing = append(missing, p)
		}
	}
	return missing
}

// opaque reports whether a path lies below a field whose members the schema does
// not describe: a map, a raw JSON value, or a type that contains itself.
func opaque(path string, fields []Field) bool {
	for prefix := path; ; {
		i := strings.LastIndex(prefix, ".")
		if i < 0 {
			return false
		}
		prefix = prefix[:i]
		f, ok := FieldAt(fields, prefix)
		if !ok {
			continue
		}
		switch f.Type {
		case TypeJSON, TypeAny:
			return true
		case TypeObject:
			// An object without described members is a map or the end of a
			// recursive type; either way its members come from the data.
			if !hasChildren(fields, prefix) {
				return true
			}
		}
	}
}

// hasChildren reports whether the field list describes a member of path.
func hasChildren(fields []Field, path string) bool {
	prefix := path + "."
	for _, f := range fields {
		if strings.HasPrefix(f.Path, prefix) {
			return true
		}
	}
	return false
}

// FieldAt returns the field with a path.
func FieldAt(fields []Field, path string) (Field, bool) {
	for _, f := range fields {
		if f.Path == path {
			return f, true
		}
	}
	return Field{}, false
}
