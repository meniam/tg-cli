package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	toon "github.com/toon-format/toon-go"
)

// Toon writes the envelope encoded as TOON and followed by a newline. Both
// success and failure envelopes go through here, so an agent reads one format
// for everything a command prints.
//
// The document is built from the JSON encoding of the value, not from the Go
// value itself: the keys, their order and the numbers are then exactly what
// --format json prints, and the two formats cannot drift apart.
func Toon(w io.Writer, v any) error {
	doc, err := marshalOrdered(v)
	if err != nil {
		return err
	}
	s, err := toon.MarshalString(align(doc))
	if err != nil {
		return fmt.Errorf("encode TOON: %w", err)
	}
	_, err = io.WriteString(w, s+"\n")
	return err
}

// obj is a JSON object with its key order kept: a Go map would lose it, and the
// order is what makes the output of two runs comparable.
type obj []field

type field struct {
	key   string
	value any
}

// MarshalJSON writes an object back as a JSON object. The ordered form is a Go
// slice, so without this method it would be written as an array of fields.
// encoding/json sorts the keys of a map, so the order is not kept here: the
// ordered form exists for TOON, which keeps it by itself.
func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		value, err := json.Marshal(f.value)
		if err != nil {
			return nil, err
		}
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalOrdered turns a value into the decoded form of its JSON encoding:
// values are obj, []any, json.Number, string, bool or nil.
func marshalOrdered(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode response: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := decodeOrdered(dec)
	if err != nil {
		return nil, fmt.Errorf("read back the response: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("read back the response: more than one JSON value")
	}
	return value, nil
}

// decodeOrdered reads one JSON value, keeping the order of object keys.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // a number, a string, a bool or nil
	}
	switch delim {
	case '{':
		object := obj{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			value, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			key, _ := keyTok.(string)
			object = append(object, field{key: key, value: value})
		}
		_, err := dec.Token() // the closing }
		return object, err
	case '[':
		items := []any{}
		for dec.More() {
			value, err := decodeOrdered(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, value)
		}
		_, err := dec.Token() // the closing ]
		return items, err
	}
	return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
}

// toonScalar turns a JSON scalar into the value the TOON encoder keeps exact.
//
// The encoder turns a float64 back into text, so an id beyond 2^53 would come
// back changed. A whole number that fits int64 goes in as int64, which the
// encoder writes exactly or quotes as a string; anything else stays a float.
func toonScalar(v any) any {
	number, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, err := number.Int64(); err == nil {
		return i
	}
	if f, err := number.Float64(); err == nil {
		return f
	}
	return number.String() // unreachable for valid JSON, kept so nothing is dropped
}

// align makes the arrays of objects share one set of keys, which is what turns
// them into the compact table form of TOON: a key an element does not have
// becomes null. Without it every element with a different set of optional
// fields is written out in full.
func align(v any) any {
	switch t := v.(type) {
	case obj:
		fields := make([]toon.Field, len(t))
		for i, f := range t {
			fields[i] = toon.Field{Key: f.key, Value: align(f.value)}
		}
		return toon.NewObject(fields...)
	case []any:
		return alignArray(t)
	}
	return toonScalar(v)
}

// alignArray gives the elements of an array one set of keys, which is what
// makes the encoder use its table form. The keys of a nested object are aligned
// the same way, as the encoder writes a nested column for them.
//
// A key an element does not have becomes null: that is the cost of the table
// form, and the reason a field missing from some elements still has a column.
func alignArray(items []any) []any {
	objects := make([]obj, 0, len(items))
	keys := []string{}
	seen := map[string]bool{}
	for _, item := range items {
		object, ok := item.(obj)
		if !ok || len(object) == 0 {
			return alignPlainArray(items) // not a table
		}
		objects = append(objects, object)
		for _, f := range object {
			if !seen[f.key] {
				seen[f.key] = true
				keys = append(keys, f.key)
			}
		}
	}
	if len(objects) == 0 {
		return []any{}
	}

	out := make([]any, len(objects))
	for i, object := range objects {
		values := make(map[string]any, len(object))
		for _, f := range object {
			values[f.key] = f.value
		}
		fields := make([]toon.Field, 0, len(keys))
		for _, key := range keys {
			// align, not the bare value: a nested object is aligned too, which
			// is what turns its keys into columns instead of a list of one
			// key per element.
			fields = append(fields, toon.Field{Key: key, Value: align(values[key])})
		}
		out[i] = toon.NewObject(fields...)
	}
	return out
}

// alignPlainArray aligns the values of an array that is not a table, so that a
// list of lists of objects is aligned as well.
func alignPlainArray(items []any) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = align(item)
	}
	return out
}
