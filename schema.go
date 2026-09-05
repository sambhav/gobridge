package gobridge

import (
	"bytes"
	"crypto/sha256"
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var pythonKeywords = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range strings.Fields("False None True and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield self _timeout str int float bool list dict bytes") {
		m[n] = true
	}
	return m
}()

var pythonReserved = func() map[string]bool {
	m := make(map[string]bool, len(pythonKeywords))
	for n := range pythonKeywords {
		m[n] = true
	}
	for _, n := range strings.Fields("call acall close aclose start lifecycle control aio RuntimeOptions DefaultControl _client command timeout max_pending expected_schema serve schema help generate_python generate_typescript") {
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
	if !identifier.MatchString(parts[0]) || pythonKeywords[parts[0]] {
		return "", fmt.Errorf("invalid or reserved field name %q", parts[0])
	}
	return parts[0], nil
}
func validateType(t reflect.Type, seen map[reflect.Type]bool) error {
	if t == reflect.TypeOf(time.Time{}) || t == reflect.TypeOf([]byte(nil)) {
		return nil
	}
	if seen[t] {
		return fmt.Errorf("recursive type %s is not supported", t)
	}
	seen[t] = true
	defer delete(seen, t)
	if t.Kind() == reflect.Pointer {
		return validateType(t.Elem(), seen)
	}
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
		fields, err := prepareStruct(t)
		if err != nil {
			return err
		}
		for _, f := range fields {
			if err = validateType(f.typ, seen); err != nil {
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
	return validateNode(raw, t, nil, "")
}

func validationAt(path string, err error) error {
	if err == nil || path == "" {
		return err
	}
	return fmt.Errorf("%s: %w", path, err)
}

func childPath(parent, field string) string {
	if parent == "" {
		return field
	}
	return parent + "." + field
}

func validateNode(raw json.RawMessage, t reflect.Type, rules *fieldRules, path string) error {
	raw = bytes.TrimSpace(raw)
	if t.Kind() == reflect.Pointer {
		if string(raw) == "null" {
			return nil
		}
		return validateNode(raw, t.Elem(), rules, path)
	}
	if string(raw) == "null" && (t.Kind() == reflect.Slice || t.Kind() == reflect.Map) {
		return nil
	}
	if string(raw) == "null" {
		return validationAt(path, fmt.Errorf("null is not allowed for %s", t))
	}
	if t == reflect.TypeOf(time.Time{}) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return validationAt(path, err)
		}
		if !regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|[+-]([01][0-9]|2[0-3]):[0-5][0-9])$`).MatchString(text) {
			return validationAt(path, fmt.Errorf("expected RFC 3339 timestamp with at most 9 fractional digits"))
		}
		var value time.Time
		return validationAt(path, json.Unmarshal(raw, &value))
	}
	if t == reflect.TypeOf([]byte(nil)) {
		if len(raw) == 0 || raw[0] != '"' {
			return validationAt(path, fmt.Errorf("bytes must be a base64 string or null"))
		}
		var value []byte
		if err := json.Unmarshal(raw, &value); err != nil {
			return validationAt(path, err)
		}
		return validationAt(path, rules.checkLength(len(value)))
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return validationAt(path, err)
		}
		metadata, err := prepareStruct(t)
		if err != nil {
			return validationAt(path, err)
		}
		for _, f := range metadata {
			v, ok := fields[f.name]
			fieldPath := childPath(path, f.name)
			if !ok {
				if f.typ.Kind() == reflect.Pointer {
					continue
				}
				return validationAt(fieldPath, fmt.Errorf("missing required field"))
			}
			if err := validateNode(v, f.typ, f.rules, fieldPath); err != nil {
				return err
			}
			delete(fields, f.name)
		}
		for n := range fields {
			return validationAt(childPath(path, n), fmt.Errorf("unknown field"))
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return validationAt(path, err)
		}
		if err := rules.checkLength(len(values)); err != nil {
			return validationAt(path, err)
		}
		for j, v := range values {
			if err := validateNode(v, t.Elem(), nil, path+"["+strconv.Itoa(j)+"]"); err != nil {
				return err
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return validationAt(path, err)
		}
		if err := rules.checkLength(len(values)); err != nil {
			return validationAt(path, err)
		}
		for key, v := range values {
			if err := validateNode(v, t.Elem(), nil, path+"["+strconv.Quote(key)+"]"); err != nil {
				return err
			}
		}
	case reflect.String:
		if rules != nil {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				return validationAt(path, err)
			}
			return validationAt(path, rules.checkLength(utf8.RuneCountInString(value)))
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return validationAt(path, rules.checkNumber(raw, t))
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
	Name        string       `json:"name"`
	Type        Type         `json:"type"`
	Description string       `json:"description,omitempty"`
	Constraints *Constraints `json:"constraints,omitempty"`
}
type Operation struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Input       Type   `json:"input"`
	Output      Type   `json:"output"`
}
type Schema struct {
	Protocol    int         `json:"protocol"`
	Hash        string      `json:"schema_hash"`
	Operations  []Operation `json:"operations"`
	Constructor *Type       `json:"constructor,omitempty"`
}

func describe(t reflect.Type) Type {
	if t == nil {
		return Type{Kind: "void"}
	}
	if t == reflect.TypeOf(time.Time{}) {
		return Type{Kind: "timestamp"}
	}
	if t == reflect.TypeOf(time.Duration(0)) {
		return Type{Kind: "duration"}
	}
	if t == reflect.TypeOf([]byte(nil)) {
		return Type{Kind: "bytes"}
	}
	s := Type{Kind: t.Kind().String()}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		e := describe(t.Elem())
		s.Elem = &e
	case reflect.Struct:
		s.Name = t.Name()
		fields, _ := prepareStruct(t)
		for _, f := range fields {
			s.Fields = append(s.Fields, Field{Name: f.name, Type: describe(f.typ), Description: f.description, Constraints: f.rules.schema()})
		}
	}
	return s
}
func (r *Registry) Schema() Schema {
	s := Schema{Protocol: 1, Operations: []Operation{}}
	for _, n := range r.names() {
		op := r.ops[n]
		s.Operations = append(s.Operations, Operation{n, op.description, op.inputSchema(), describe(op.out)})
	}
	data, _ := json.Marshal(s.Operations)
	if r.constructor != nil {
		t := describe(r.constructor.config)
		s.Constructor = &t
		data, _ = json.Marshal(struct {
			Operations  []Operation `json:"operations"`
			Constructor *Type       `json:"constructor"`
		}{s.Operations, s.Constructor})
	}
	s.Hash = fmt.Sprintf("%x", sha256.Sum256(data))
	return s
}
