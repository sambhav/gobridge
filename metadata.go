package gobridge

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Constraints describes inclusive limits on a non-null wire field. JSON numbers
// preserve exact integer bounds; an empty Number means the bound is absent.
// Length counts Unicode code points for strings and entries for slices/maps.
type Constraints struct {
	Minimum   json.Number `json:"minimum,omitempty"`
	Maximum   json.Number `json:"maximum,omitempty"`
	MinLength *int        `json:"min_length,omitempty"`
	MaxLength *int        `json:"max_length,omitempty"`
}

type fieldMetadata struct {
	required, optional, nonNull bool
	name                        string
	typ                         reflect.Type
	description                 string
	rules                       *fieldRules
}

// Numeric and length comparisons use these typed values. Parsing tags and
// numeric bounds is registration work, never part of request handling.
type fieldRules struct {
	minimum, maximum           json.Number
	minInt, maxInt             int64
	minUint, maxUint           uint64
	minFloat, maxFloat         float64
	minLength, maxLength       int
	hasMinLength, hasMaxLength bool
}

func (r *fieldRules) schema() *Constraints {
	if r == nil {
		return nil
	}
	c := &Constraints{Minimum: r.minimum, Maximum: r.maximum}
	if r.hasMinLength {
		n := r.minLength
		c.MinLength = &n
	}
	if r.hasMaxLength {
		n := r.maxLength
		c.MaxLength = &n
	}
	return c
}

var structMetadata sync.Map // map[reflect.Type][]fieldMetadata; immutable after publication

var decimalBound = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func prepareStruct(t reflect.Type) ([]fieldMetadata, error) {
	if cached, ok := structMetadata.Load(t); ok {
		return cached.([]fieldMetadata), nil
	}
	fields := make([]fieldMetadata, t.NumField())
	names := make(map[string]bool, t.NumField())
	for j := 0; j < t.NumField(); j++ {
		f := t.Field(j)
		name, err := fieldName(f)
		if err != nil {
			return nil, err
		}
		if names[name] {
			return nil, fmt.Errorf("duplicate JSON field %s", name)
		}
		names[name] = true
		rules, err := parseFieldRules(f.Type, f.Tag.Get("validate"))
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t, f.Name, err)
		}
		required, optional, nonNull := false, false, false
		if tag, ok := f.Tag.Lookup("required"); ok {
			if tag != "true" && tag != "false" {
				return nil, fmt.Errorf("%s: required must be true or false", f.Name)
			}
			required = tag == "true"
			optional = tag == "false"
		}
		if tag, ok := f.Tag.Lookup("nullable"); ok {
			if tag != "true" && tag != "false" {
				return nil, fmt.Errorf("%s: nullable must be true or false", f.Name)
			}
			nonNull = tag == "false"
		}
		if required && strings.Contains(f.Tag.Get("json"), ",omitempty") {
			return nil, fmt.Errorf("%s: required fields cannot use omitempty", f.Name)
		}
		fields[j] = fieldMetadata{required: required, optional: optional, nonNull: nonNull, name: name, typ: f.Type, description: f.Tag.Get("doc"), rules: rules}
	}
	actual, _ := structMetadata.LoadOrStore(t, fields)
	return actual.([]fieldMetadata), nil
}

func parseFieldRules(t reflect.Type, tag string) (*fieldRules, error) {
	if strings.TrimSpace(tag) == "" {
		return nil, nil
	}
	seenPointers := map[reflect.Type]bool{}
	for t.Kind() == reflect.Pointer {
		if seenPointers[t] {
			return nil, fmt.Errorf("constraints on recursive pointers are unsupported")
		}
		seenPointers[t] = true
		t = t.Elem()
	}
	if wire, err := adaptedType(t); err != nil {
		return nil, err
	} else if wire != nil {
		t = wire
	}
	isUnsigned := t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64
	isInteger := t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64
	isFloat := t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64
	hasLength := t.Kind() == reflect.String || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map
	r := &fieldRules{}
	seen := map[string]bool{}
	for _, part := range strings.Split(tag, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("validation rule %q requires name=value", strings.TrimSpace(part))
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate validation rule %q", key)
		}
		seen[key] = true
		switch key {
		case "min", "max":
			if !isInteger && !isFloat && !isUnsigned {
				return nil, fmt.Errorf("%s applies only to numeric fields, not %s", key, t)
			}
			var number json.Number
			if isUnsigned {
				n, err := strconv.ParseUint(value, 10, t.Bits())
				if err != nil {
					return nil, fmt.Errorf("%s must be an unsigned integer fitting %s", key, t)
				}
				number = json.Number(strconv.FormatUint(n, 10))
				if key == "min" {
					r.minUint = n
				} else {
					r.maxUint = n
				}
			} else if isInteger {
				n, err := strconv.ParseInt(value, 10, t.Bits())
				if err != nil {
					return nil, fmt.Errorf("%s must be a decimal integer fitting %s", key, t)
				}
				number = json.Number(strconv.FormatInt(n, 10))
				if key == "min" {
					r.minInt = n
				} else {
					r.maxInt = n
				}
			} else {
				if !decimalBound.MatchString(value) {
					return nil, fmt.Errorf("%s must be a finite decimal number fitting %s", key, t)
				}
				n, err := strconv.ParseFloat(value, t.Bits())
				if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					return nil, fmt.Errorf("%s must be a finite number fitting %s", key, t)
				}
				number = json.Number(strconv.FormatFloat(n, 'g', -1, t.Bits()))
				if key == "min" {
					r.minFloat = n
				} else {
					r.maxFloat = n
				}
			}
			if key == "min" {
				r.minimum = number
			} else {
				r.maximum = number
			}
		case "minlen", "maxlen":
			if !hasLength {
				return nil, fmt.Errorf("%s applies only to strings, slices, or maps, not %s", key, t)
			}
			n, err := strconv.ParseInt(value, 10, 32)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s must be an integer from 0 to 2147483647", key)
			}
			if key == "minlen" {
				r.minLength = int(n)
				r.hasMinLength = true
			} else {
				r.maxLength = int(n)
				r.hasMaxLength = true
			}
		default:
			return nil, fmt.Errorf("unknown validation rule %q", key)
		}
	}
	if r.minimum != "" && r.maximum != "" {
		if (isUnsigned && r.minUint > r.maxUint) || (isInteger && r.minInt > r.maxInt) || (isFloat && r.minFloat > r.maxFloat) {
			return nil, fmt.Errorf("min must not exceed max")
		}
	}
	if r.hasMinLength && r.hasMaxLength && r.minLength > r.maxLength {
		return nil, fmt.Errorf("minlen must not exceed maxlen")
	}
	return r, nil
}

func (r *fieldRules) checkLength(n int) error {
	if r == nil {
		return nil
	}
	if r.hasMinLength && n < r.minLength {
		return fmt.Errorf("length must be at least %d", r.minLength)
	}
	if r.hasMaxLength && n > r.maxLength {
		return fmt.Errorf("length must be at most %d", r.maxLength)
	}
	return nil
}

func (r *fieldRules) checkNumber(raw []byte, t reflect.Type) error {
	if r == nil {
		return nil
	}
	if t.Kind() >= reflect.Uint && t.Kind() <= reflect.Uint64 {
		n, err := strconv.ParseUint(string(raw), 10, t.Bits())
		if err != nil {
			return fmt.Errorf("must be an unsigned integer representable as %s", t)
		}
		if r.minimum != "" && n < r.minUint {
			return fmt.Errorf("must be at least %s", r.minimum)
		}
		if r.maximum != "" && n > r.maxUint {
			return fmt.Errorf("must be at most %s", r.maximum)
		}
	} else if t.Kind() >= reflect.Int && t.Kind() <= reflect.Int64 {
		n, err := strconv.ParseInt(string(raw), 10, t.Bits())
		if err != nil {
			return fmt.Errorf("must be an integer representable as %s", t)
		}
		if r.minimum != "" && n < r.minInt {
			return fmt.Errorf("must be at least %s", r.minimum)
		}
		if r.maximum != "" && n > r.maxInt {
			return fmt.Errorf("must be at most %s", r.maximum)
		}
	} else {
		n, err := strconv.ParseFloat(string(raw), t.Bits())
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return fmt.Errorf("must be a finite number representable as %s", t)
		}
		if r.minimum != "" && n < r.minFloat {
			return fmt.Errorf("must be at least %s", r.minimum)
		}
		if r.maximum != "" && n > r.maxFloat {
			return fmt.Errorf("must be at most %s", r.maximum)
		}
	}
	return nil
}
