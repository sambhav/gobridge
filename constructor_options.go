package gobridge

import (
	"fmt"
	"reflect"
	"strings"
)

// ConstructorOption exposes a Go option factory as an optional
// constructor keyword. Omission preserves the Go constructor's defaults. A
// factory may return Option or (Option, error); it runs only at initialization.
// For multiple arguments, supply their wire names in declaration order. They
// become a typed <OptionName>Options object; a single argument stays a scalar.
func ConstructorOption(name string, factory any, paramNames ...string) OptionFactory {
	return OptionFactory{name, factory, append([]string(nil), paramNames...)}
}

// OptionFactory is an explicitly exposed constructor keyword and its Go factory.
// Create values with ConstructorOption.
type OptionFactory struct {
	name    string
	factory any
	params  []string
}

// newFunctionalObject adapts func(...Option) *T (or (*T, error)), optionally with
// context.Context, to typed Python keyword arguments and TypeScript options.
// Factories apply in declaration order. Multi-argument factories use a grouped
// options object, with fields named by ConstructorOption parameter names.
func newFunctionalObject(r *Registry, fn any, options ...OptionFactory) (*Object, error) {
	f := reflect.ValueOf(fn)
	if !f.IsValid() || f.Kind() != reflect.Func || f.IsNil() || !f.Type().IsVariadic() {
		return nil, fmt.Errorf("functional-options constructor must be a non-nil variadic function")
	}
	t := f.Type()
	offset := 0
	if t.NumIn() > 0 && t.In(0) == contextType {
		offset = 1
	}
	if t.NumIn() != offset+1 || t.NumOut() < 1 || t.NumOut() > 2 || (t.NumOut() == 2 && t.Out(1) != errorType) {
		return nil, fmt.Errorf("constructor must accept only optional context.Context and ...Option, and return *T or (*T, error)")
	}
	receiver := t.Out(0)
	if receiver.Kind() != reflect.Pointer || receiver.Elem().Kind() != reflect.Struct || receiver.Elem().Name() == "" {
		return nil, fmt.Errorf("constructor must return a pointer to a named struct")
	}
	optionType := t.In(offset).Elem()
	fields := make([]reflect.StructField, len(options))
	factories := make([]reflect.Value, len(options))
	generatedTypes := map[reflect.Type]string{}
	seen := map[string]bool{}
	for i, option := range options {
		if !identifier.MatchString(option.name) || pythonKeywords[option.name] || option.name == "command" || seen[option.name] {
			return nil, fmt.Errorf("invalid or duplicate constructor option %q", option.name)
		}
		seen[option.name] = true
		factory := reflect.ValueOf(option.factory)
		if !factory.IsValid() || factory.Kind() != reflect.Func || factory.IsNil() {
			return nil, fmt.Errorf("option %s must be a function", option.name)
		}
		ft := factory.Type()
		if ft.IsVariadic() || ft.NumIn() < 1 || ft.NumOut() < 1 || ft.NumOut() > 2 || ft.Out(0) != optionType || (ft.NumOut() == 2 && ft.Out(1) != errorType) {
			return nil, fmt.Errorf("option %s must take wire values and return %s or (%s, error)", option.name, optionType, optionType)
		}
		input := ft.In(0)
		for j := 0; j < ft.NumIn(); j++ {
			if err := validateType(ft.In(j), map[reflect.Type]bool{}); err != nil {
				return nil, fmt.Errorf("option %s parameter %d: %w", option.name, j+1, err)
			}
		}
		if len(option.params) > 0 && len(option.params) != ft.NumIn() {
			return nil, fmt.Errorf("option %s expects %d parameter names", option.name, ft.NumIn())
		}
		if ft.NumIn() > 1 {
			if len(option.params) != ft.NumIn() {
				return nil, fmt.Errorf("option %s requires %d parameter names", option.name, ft.NumIn())
			}
			members := make([]reflect.StructField, ft.NumIn())
			seenParams := map[string]bool{}
			for j, name := range option.params {
				if !identifier.MatchString(name) || pythonKeywords[name] || seenParams[name] {
					return nil, fmt.Errorf("option %s: invalid or duplicate parameter %q", option.name, name)
				}
				seenParams[name] = true
				// Include the option index to distinguish equal-shaped groups.
				members[j] = reflect.StructField{Name: fmt.Sprintf("Option%dArg%d", i, j), Type: ft.In(j), Tag: reflect.StructTag(`json:"` + name + `"`)}
			}
			input = reflect.StructOf(members)
			model := ""
			for _, part := range strings.Split(option.name, "_") {
				if part != "" {
					model += strings.ToUpper(part[:1]) + part[1:]
				}
			}
			generatedTypes[input] = model + "Options"
		}
		fields[i] = reflect.StructField{Name: fmt.Sprintf("Option%d", i), Type: reflect.PointerTo(input), Tag: reflect.StructTag(`json:"` + option.name + `,omitempty"`)}
		factories[i] = factory
	}
	config := reflect.StructOf(fields)
	generatedTypes[config] = receiver.Elem().Name() + "Config"
	wrapperType := reflect.FuncOf([]reflect.Type{contextType, config}, []reflect.Type{receiver, errorType}, false)
	wrapper := reflect.MakeFunc(wrapperType, func(args []reflect.Value) []reflect.Value {
		values := reflect.MakeSlice(t.In(offset), 0, len(options))
		for i, factory := range factories {
			value := args[1].Field(i)
			if value.IsNil() {
				continue
			}
			arguments := []reflect.Value{value.Elem()}
			if factory.Type().NumIn() > 1 {
				arguments = make([]reflect.Value, factory.Type().NumIn())
				for j := range arguments {
					arguments[j] = value.Elem().Field(j)
				}
			}
			result := factory.Call(arguments)
			if len(result) == 2 && !result[1].IsNil() {
				return []reflect.Value{reflect.Zero(receiver), result[1]}
			}
			values = reflect.Append(values, result[0])
		}
		call := []reflect.Value{values}
		if offset == 1 {
			call = append([]reflect.Value{args[0]}, call...)
		}
		result := f.CallSlice(call)
		if len(result) == 1 {
			result = append(result, reflect.Zero(errorType))
		}
		return result
	})
	object, err := newObject(r, wrapper.Interface(), generatedTypes)
	return object, err
}
