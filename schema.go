package gobridge

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

var pythonReserved = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield self _timeout call acall close aclose start _client command timeout max_pending expected_schema serve schema help generate_python") {
		m[n] = true
	}
	return m
}()

func fieldName(f reflect.StructField) (string, error) {
	parts := strings.Split(f.Tag.Get("json"), ",")
	if f.Anonymous || !f.IsExported() || parts[0] == "" || parts[0] == "-" {
		return "", fmt.Errorf("%s: exported, non-embedded fields with explicit JSON names required", f.Name)
	}
	for _, p := range parts[1:] {
		if p != "omitempty" || f.Type.Kind() != reflect.Pointer {
			return "", fmt.Errorf("%s: only pointer omitempty is supported", f.Name)
		}
	}
	if !identifier.MatchString(parts[0]) || pythonReserved[parts[0]] {
		return "", fmt.Errorf("invalid or reserved field name %q", parts[0])
	}
	return parts[0], nil
}
func validateType(t reflect.Type, seen map[reflect.Type]bool) error {
	if seen[t] {
		return fmt.Errorf("recursive type %s is not supported", t)
	}
	seen[t] = true
	defer delete(seen, t)
	marshal := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshal := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textMarshal := reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshal := reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	if t.Implements(textMarshal) || reflect.PointerTo(t).Implements(textMarshal) || t.Implements(textUnmarshal) || reflect.PointerTo(t).Implements(textUnmarshal) {
		return fmt.Errorf("custom text type %s needs an explicit adapter", t)
	}
	if t.Implements(marshal) || reflect.PointerTo(t).Implements(marshal) || t.Implements(unmarshal) || reflect.PointerTo(t).Implements(unmarshal) {
		return fmt.Errorf("custom JSON type %s needs an explicit adapter", t)
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Pointer:
		return validateType(t.Elem(), seen)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return fmt.Errorf("[]byte requires an explicit base64 string adapter")
		}
		return validateType(t.Elem(), seen)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("map keys must be strings")
		}
		return validateType(t.Elem(), seen)
	case reflect.Struct:
		if t.Name() == "" {
			return fmt.Errorf("anonymous structs are unsupported")
		}
		names := map[string]bool{}
		for j := 0; j < t.NumField(); j++ {
			f := t.Field(j)
			n, err := fieldName(f)
			if err != nil {
				return err
			}
			if names[n] {
				return fmt.Errorf("duplicate JSON field %s", n)
			}
			names[n] = true
			if err = validateType(f.Type, seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported wire type %s", t)
	}
}

// validateValue prevents encoding/json silently accepting missing required
// fields or null for scalar values. Numeric range/type checks remain in Go.
func validateValue(raw json.RawMessage, t reflect.Type) error {
	raw = bytes.TrimSpace(raw)
	if t.Kind() == reflect.Pointer {
		if string(raw) == "null" {
			return nil
		}
		return validateValue(raw, t.Elem())
	}
	if string(raw) == "null" && (t.Kind() == reflect.Slice || t.Kind() == reflect.Map) {
		return nil
	}
	if string(raw) == "null" {
		return fmt.Errorf("null is not allowed for %s", t)
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return err
		}
		for j := 0; j < t.NumField(); j++ {
			f := t.Field(j)
			n, _ := fieldName(f)
			v, ok := fields[n]
			if !ok {
				if f.Type.Kind() == reflect.Pointer {
					continue
				}
				return fmt.Errorf("missing field %s", n)
			}
			if err := validateValue(v, f.Type); err != nil {
				return fmt.Errorf("%s: %w", n, err)
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := validateValue(v, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := validateValue(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

type Type struct {
	Kind   string  `json:"kind"`
	Name   string  `json:"name,omitempty"`
	Elem   *Type   `json:"elem,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}
type Field struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}
type Operation struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Input       Type   `json:"input"`
	Output      Type   `json:"output"`
}
type Schema struct {
	Protocol   int         `json:"protocol"`
	Hash       string      `json:"schema_hash"`
	Operations []Operation `json:"operations"`
}

func describe(t reflect.Type) Type {
	s := Type{Kind: t.Kind().String()}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		e := describe(t.Elem())
		s.Elem = &e
	case reflect.Struct:
		s.Name = t.Name()
		for j := 0; j < t.NumField(); j++ {
			f := t.Field(j)
			n, _ := fieldName(f)
			s.Fields = append(s.Fields, Field{n, describe(f.Type)})
		}
	}
	return s
}
func (r *Registry) Schema() Schema {
	s := Schema{Protocol: 1, Operations: []Operation{}}
	for _, n := range r.names() {
		op := r.ops[n]
		s.Operations = append(s.Operations, Operation{n, op.description, describe(op.in), describe(op.out)})
	}
	data, _ := json.Marshal(s.Operations)
	s.Hash = fmt.Sprintf("%x", sha256.Sum256(data))
	return s
}
