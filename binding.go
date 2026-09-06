package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Bind exposes an ordinary Go function or bound method. Supply every parameter
// name explicitly, except an optional leading context.Context. Supported results
// are T, (T, error), error, or no results. Pointer parameters are optional.
// Reflection adapts calls; Register retains its direct typed invocation path.
func Bind(r *Registry, name string, fn any, paramNames ...string) error {
	if err := r.checkName(name); err != nil {
		return err
	}
	op, err := compileBinding(name, reflect.ValueOf(fn), nil, paramNames)
	if err != nil {
		return fmt.Errorf("bind %s: %w", name, err)
	}
	return r.add(op)
}

// receiver supplies the initialized process-owned receiver for method expressions.
func compileBinding(name string, fn reflect.Value, receiver func() reflect.Value, paramNames []string) (operation, error) {
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.IsNil() {
		return operation{}, fmt.Errorf("expected a non-nil function")
	}
	t := fn.Type()
	if t.NumIn() > 0 && t.NumOut() == 1 && t.Out(0) == errorType && !t.IsVariadic() {
		last := t.In(t.NumIn() - 1)
		if last.Kind() == reflect.Func && last.NumIn() == 1 && last.NumOut() == 1 && last.Out(0) == errorType && !last.IsVariadic() {
			return compileStreamBinding(name, fn, receiver, paramNames)
		}
	}
	if t.IsVariadic() {
		return operation{}, fmt.Errorf("variadic functions need an explicit slice adapter")
	}
	offset := 0
	if receiver != nil {
		offset++
	}
	hasContext := t.NumIn() > offset && t.In(offset) == contextType
	if hasContext {
		offset++
	}
	if len(paramNames) != t.NumIn()-offset {
		return operation{}, fmt.Errorf("expected %d explicit parameter names, got %d", t.NumIn()-offset, len(paramNames))
	}
	fields := make([]reflect.StructField, len(paramNames))
	seen := make(map[string]bool, len(paramNames))
	for j, n := range paramNames {
		if !identifier.MatchString(n) || pythonKeywords[n] {
			return operation{}, fmt.Errorf("invalid or reserved parameter name %q", n)
		}
		if seen[n] {
			return operation{}, fmt.Errorf("duplicate parameter name %q", n)
		}
		seen[n] = true
		argType := t.In(j + offset)
		if err := validateType(argType, make(map[reflect.Type]bool)); err != nil {
			return operation{}, fmt.Errorf("parameter %s: %w", n, err)
		}
		fields[j] = reflect.StructField{Name: fmt.Sprintf("Arg%d", j), Type: argType, Tag: reflect.StructTag(`json:"` + n + `"`)}
	}
	var output reflect.Type
	hasError := false
	switch t.NumOut() {
	case 0:
	case 1:
		if t.Out(0) == errorType {
			hasError = true
		} else {
			output = t.Out(0)
		}
	case 2:
		if t.Out(1) != errorType {
			return operation{}, fmt.Errorf("second result must be error")
		}
		output, hasError = t.Out(0), true
	default:
		return operation{}, fmt.Errorf("expected T, (T, error), error, or no results")
	}
	if output != nil {
		if err := validateType(output, make(map[reflect.Type]bool)); err != nil {
			return operation{}, fmt.Errorf("result: %w", err)
		}
	}
	input := reflect.StructOf(fields)
	if _, err := prepareStruct(input); err != nil {
		return operation{}, err
	}
	var inputName strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part != "" {
			inputName.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	inputName.WriteString("Params")
	op := operation{name: name, in: input, out: output, inName: inputName.String()}
	op.call = func(ctx context.Context, raw json.RawMessage) (any, error) {
		value, err := decodeInput(raw, input)
		if err != nil {
			return nil, Failure("invalid_argument", err.Error())
		}
		args := make([]reflect.Value, 0, t.NumIn())
		if receiver != nil {
			args = append(args, receiver())
		}
		if hasContext {
			args = append(args, reflect.ValueOf(ctx))
		}
		for j := range paramNames {
			args = append(args, value.Field(j))
		}
		results := fn.Call(args)
		if hasError && !results[len(results)-1].IsNil() {
			return nil, results[len(results)-1].Interface().(error)
		}
		if output == nil {
			return nil, nil
		}
		return results[0].Interface(), nil
	}
	return op, nil
}

func decodeInput(raw json.RawMessage, t reflect.Type) (reflect.Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	if err := validateValue(raw, t); err != nil {
		return reflect.Value{}, err
	}
	v := reflect.New(t)
	// Structural validation above is strict; Unmarshal supplies scalar/range
	// checks and rejects multiple JSON values without a streaming decoder.
	if err := json.Unmarshal(raw, v.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return v.Elem(), nil
}
