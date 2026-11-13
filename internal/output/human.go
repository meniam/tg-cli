package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxCell bounds one value in a line or a table cell, so a long text does not
// bury the rest of the answer.
const maxCell = 60

// Human writes the envelope for a person to read.
//
// A success is its data alone: an object as "key: value" lines, a list of
// objects as a table of its columns, a list of plain values one per line. A
// failure is one line with the message and the machine name of the exit code,
// the same name --format json puts in error.code.
func Human(w io.Writer, v any) error {
	doc, err := marshalOrdered(v)
	if err != nil {
		return err
	}
	envelope, ok := doc.(obj)
	if !ok {
		return fmt.Errorf("encode response: the envelope is not an object")
	}
	var b strings.Builder
	if ok, _ := envelope.get("ok").(bool); ok {
		writeHuman(&b, envelope.get("data"), 0)
	} else {
		failure, _ := envelope.get("error").(obj)
		code, _ := failure.get("code").(string)
		message, _ := failure.get("message").(string)
		fmt.Fprintf(&b, "error (%s): %s\n", code, message)
	}
	_, err = io.WriteString(w, b.String())
	return err
}

// writeHuman writes a value at an indentation level.
func writeHuman(b *strings.Builder, v any, depth int) {
	indent := strings.Repeat("  ", depth)
	switch t := v.(type) {
	case obj:
		if len(t) == 0 {
			fmt.Fprintf(b, "%s(nothing)\n", indent)
			return
		}
		width := keyWidth(t)
		for _, f := range t {
			if isScalar(f.value) {
				fmt.Fprintf(b, "%s%s: %s\n", indent, pad(f.key, width), scalar(f.value))
				continue
			}
			fmt.Fprintf(b, "%s%s:\n", indent, pad(f.key, width))
			writeHuman(b, f.value, depth+1)
		}
	case []any:
		writeHumanList(b, t, depth)
	default:
		fmt.Fprintf(b, "%s%s\n", indent, scalar(t))
	}
}

// writeHumanList writes a list: a table when its items are objects with the same
// keys, one item per line otherwise.
func writeHumanList(b *strings.Builder, items []any, depth int) {
	indent := strings.Repeat("  ", depth)
	if len(items) == 0 {
		fmt.Fprintf(b, "%s(nothing)\n", indent)
		return
	}
	if keys, rows, ok := tableOf(items); ok {
		writeTable(b, indent, keys, rows)
		return
	}
	for _, item := range items {
		if isScalar(item) {
			fmt.Fprintf(b, "%s- %s\n", indent, scalar(item))
			continue
		}
		fmt.Fprintf(b, "%s-\n", indent)
		writeHuman(b, item, depth+1)
	}
}

// tableOf turns a list of objects into columns and rows, or reports false when
// the items are not objects or do not share one set of keys in one order.
func tableOf(items []any) (keys []string, rows [][]string, ok bool) {
	objects := make([]obj, 0, len(items))
	for i, item := range items {
		object, isObject := item.(obj)
		if !isObject {
			return nil, nil, false
		}
		if i == 0 {
			for _, f := range object {
				keys = append(keys, f.key)
			}
		} else if !sameKeys(keys, object) {
			return nil, nil, false
		}
		objects = append(objects, object)
	}
	for _, object := range objects {
		line := make([]string, 0, len(keys))
		for _, key := range keys {
			value := object.get(key)
			if !isScalar(value) {
				line = append(line, "{...}") // the shape of a nested value, not its content
				continue
			}
			line = append(line, scalar(value))
		}
		rows = append(rows, line)
	}
	return keys, rows, true
}

// sameKeys reports whether an object has the keys of the first item, in the same
// order: a different set of optional fields makes each item a block of its own
// instead of a row of the table.
func sameKeys(keys []string, object obj) bool {
	if len(object) != len(keys) {
		return false
	}
	for i, key := range keys {
		if object[i].key != key {
			return false
		}
	}
	return true
}

// writeTable writes the header and the rows as aligned columns.
func writeTable(b *strings.Builder, indent string, keys []string, rows [][]string) {
	widths := make([]int, len(keys))
	for i, key := range keys {
		widths[i] = utf8.RuneCountInString(key)
	}
	for _, line := range rows {
		for i, cell := range line {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}
	fmt.Fprintf(b, "%s%s\n", indent, joinPadded(keys, widths))
	for _, line := range rows {
		fmt.Fprintf(b, "%s%s\n", indent, joinPadded(line, widths))
	}
}

// joinPadded joins cells, each padded to its column width. The trailing spaces
// of the last cell are dropped, so two lines never differ by invisible spaces.
func joinPadded(cells []string, widths []int) string {
	var b strings.Builder
	for i, cell := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(cell)
		if i < len(cells)-1 {
			b.WriteString(strings.Repeat(" ", max(widths[i]-utf8.RuneCountInString(cell), 0)))
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// keyWidth is the width of the longest key of an object.
func keyWidth(object obj) int {
	width := 0
	for _, f := range object {
		width = max(width, utf8.RuneCountInString(f.key))
	}
	return width
}

// pad fills a key to the width of the widest key, so the values line up.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(width-utf8.RuneCountInString(s), 0))
}

// isScalar reports whether a value is written on one line.
func isScalar(v any) bool {
	switch v.(type) {
	case nil, string, bool, int64, float64:
		return true
	}
	return false
}

// scalar renders a value that fits on one line. A string with a line break or a
// value too long for the line is cut, not escaped: a person reads this.
func scalar(v any) string {
	switch t := v.(type) {
	case nil:
		return "-"
	case string:
		return cut(t)
	case float64:
		// 'f' keeps a small rank readable (-1e-06 is -0.000001 for a person),
		// and four decimals are as much as a person reads of a rank.
		return strconv.FormatFloat(t, 'f', 4, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return fmt.Sprint(v)
}

// cut shortens a value that does not fit in maxCell characters, counted as runes
// so a Cyrillic title is not cut earlier than a Latin one, and never inside a
// rune.
func cut(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if utf8.RuneCountInString(s) <= maxCell {
		return s
	}
	return string([]rune(s)[:maxCell]) + "..."
}

// get returns the value stored under a key of an object, or nil.
func (o obj) get(key string) any {
	for _, f := range o {
		if f.key == key {
			return f.value
		}
	}
	return nil
}
