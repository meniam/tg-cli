// Package tgraw serializes Telegram objects for local storage.
package tgraw

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tdp"
	"github.com/gotd/td/tg"
)

type namedObject interface {
	tdp.Object
	TypeName() string
}

// Marshal encodes a Telegram object, removing access hashes and authorization hashes.
func Marshal(obj tdp.Object) (json.RawMessage, error) {
	if obj == nil || (reflect.ValueOf(obj).Kind() == reflect.Ptr && reflect.ValueOf(obj).IsNil()) {
		return nil, fmt.Errorf("nil Telegram object")
	}
	named, ok := obj.(namedObject)
	if !ok {
		return nil, fmt.Errorf("object %T has no TypeName", obj)
	}
	b, err := appendObject(nil, named, reflect.ValueOf(obj))
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("invalid JSON output")
	}
	return json.RawMessage(b), nil
}

func appendJSON(b []byte, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, data...), nil
}

func appendValue(b []byte, v reflect.Value) ([]byte, error) {
	if !v.IsValid() {
		return nil, fmt.Errorf("invalid value")
	}
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, fmt.Errorf("nil interface")
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, fmt.Errorf("nil pointer %s", v.Type())
	}
	if v.CanInterface() {
		// TL JSONValue carries ordinary JSON, including false, zero and null.
		if j, ok := v.Interface().(tg.JSONValueClass); ok {
			return appendJSONValue(b, j)
		}
		// Int256 is a fixed-width byte sequence encoded as lowercase hexadecimal.
		if n, ok := v.Interface().(bin.Int256); ok {
			return appendJSON(b, hex.EncodeToString(n[:]))
		}
		if obj, ok := v.Interface().(namedObject); ok {
			return appendObject(b, obj, v)
		}
	}
	if v.Kind() == reflect.Ptr {
		return appendValue(b, v.Elem())
	}
	if v.Kind() == reflect.Struct && v.CanAddr() && v.Addr().CanInterface() {
		if obj, ok := v.Addr().Interface().(namedObject); ok {
			return appendObject(b, obj, v)
		}
	}
	switch v.Kind() {
	case reflect.Bool:
		return strconv.AppendBool(b, v.Bool()), nil
	case reflect.String:
		return appendJSON(b, v.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(b, v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(b, v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return appendJSON(b, v.Interface())
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return appendJSON(b, v.Bytes())
		}
		b = append(b, '[')
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			b, err = appendValue(b, v.Index(i))
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
		}
		return append(b, ']'), nil
	default:
		return nil, fmt.Errorf("unsupported value type %s", v.Type())
	}
}

func appendObject(b []byte, obj namedObject, v reflect.Value) ([]byte, error) {
	name := obj.TypeName()
	info := obj.TypeInfo()
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil, fmt.Errorf("object %s is not a struct", name)
	}
	b = append(b, '{')
	b = append(b, '"', '_', '"', ':')
	var err error
	b, err = appendJSON(b, name)
	if err != nil {
		return nil, err
	}
	for _, field := range info.Fields {
		if field.Null || field.SchemaName == "flags" || field.SchemaName == "access_hash" || (name == "authorization" && field.SchemaName == "hash") {
			continue
		}
		f := v.FieldByName(field.Name)
		if !f.IsValid() || !f.CanInterface() {
			return nil, fmt.Errorf("%s: missing field %s", name, field.Name)
		}
		if isZero(f) {
			continue
		}
		b = append(b, ',')
		b, err = appendJSON(b, field.SchemaName)
		if err != nil {
			return nil, err
		}
		b = append(b, ':')
		b, err = appendValue(b, f)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", name, field.Name, err)
		}
	}
	return append(b, '}'), nil
}

func appendJSONValue(b []byte, value tg.JSONValueClass) ([]byte, error) {
	switch j := value.(type) {
	case *tg.JSONNull:
		return append(b, "null"...), nil
	case *tg.JSONBool:
		return strconv.AppendBool(b, j.Value), nil
	case *tg.JSONNumber:
		return appendJSON(b, j.Value)
	case *tg.JSONString:
		return appendJSON(b, j.Value)
	case *tg.JSONArray:
		b = append(b, '[')
		for i, v := range j.Value {
			if i > 0 {
				b = append(b, ',')
			}
			var err error
			b, err = appendValue(b, reflect.ValueOf(v))
			if err != nil {
				return nil, err
			}
		}
		return append(b, ']'), nil
	case *tg.JSONObject:
		b = append(b, '{')
		first := true
		for _, v := range j.Value {
			if v.Key == "access_hash" {
				continue
			}
			if !first {
				b = append(b, ',')
			}
			first = false
			var err error
			b, err = appendJSON(b, v.Key)
			if err != nil {
				return nil, err
			}
			b = append(b, ':')
			b, err = appendValue(b, reflect.ValueOf(v.Value))
			if err != nil {
				return nil, err
			}
		}
		return append(b, '}'), nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %T", value)
	}
}

func isZero(v reflect.Value) bool {
	for v.Kind() == reflect.Interface {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	return v.IsZero() || (v.Kind() == reflect.Slice && v.Len() == 0)
}
