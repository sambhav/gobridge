package gobridge

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

type constructor struct {
	names      map[reflect.Type]string
	config     reflect.Type
	initialize func(context.Context, json.RawMessage) error
}

// Object binds method expressions to one constructor-created receiver per daemon.
// Register all methods before serving; mutable receiver state must be safe for
// concurrent method calls. Constructors never run during schema generation.
type Object struct {
	registry     *Registry
	receiverType reflect.Type
	instance     reflect.Value
}

// NewObject declares the registry's constructor. It accepts func(Config) *T or
// func(Config) (*T, error), optionally with a leading context.Context. Config must
// be a named wire struct and *T a pointer to a named struct. One constructor is
// supported per registry; Bind can also register ordinary functions alongside it.
// A variadic func(...Option) constructor uses ConstructorOption factories to
// expose its options; multi-parameter factories become typed keyword groups.
func NewObject(r *Registry, fn any, options ...OptionFactory) (*Object, error) {
	f := reflect.ValueOf(fn)
	if f.IsValid() && f.Kind() == reflect.Func && f.Type().IsVariadic() {
		return newFunctionalObject(r, fn, options...)
	}
	if len(options) != 0 {
		return nil, fmt.Errorf("option factories require a variadic constructor")
	}
	return newObject(r, fn, nil)
}

func newObject(r *Registry, fn any, generated map[reflect.Type]string) (*Object, error) {
	if r == nil {
		return nil, fmt.Errorf("registry must not be nil")
	}
	if r.constructor != nil {
		return nil, fmt.Errorf("a constructor is already registered")
	}
	f := reflect.ValueOf(fn)
	if !f.IsValid() || f.Kind() != reflect.Func || f.IsNil() || f.Type().IsVariadic() {
		return nil, fmt.Errorf("constructor must be a non-nil, non-variadic function")
	}
	t := f.Type()
	offset := 0
	if t.NumIn() > 0 && t.In(0) == contextType {
		offset = 1
	}
	if t.NumIn() != offset+1 {
		return nil, fmt.Errorf("constructor requires one named Config struct and optional leading context.Context")
	}
	config := t.In(offset)
	if config.Kind() != reflect.Struct || (config.Name() == "" && generated == nil) {
		return nil, fmt.Errorf("constructor Config must be a named struct")
	}
	var configErr error
	if generated != nil {
		var validateGenerated func(reflect.Type) error
		validateGenerated = func(t reflect.Type) error {
			if t.Kind() == reflect.Pointer {
				return validateGenerated(t.Elem())
			}
			if _, ok := generated[t]; !ok {
				return validateType(t, make(map[reflect.Type]bool))
			}
			if _, err := prepareStruct(t); err != nil {
				return err
			}
			for j := 0; j < t.NumField(); j++ {
				if err := validateGenerated(t.Field(j).Type); err != nil {
					return err
				}
			}
			return nil
		}
		configErr = validateGenerated(config)
	} else {
		configErr = validateType(config, make(map[reflect.Type]bool))
	}
	if err := configErr; err != nil {
		return nil, fmt.Errorf("constructor Config: %w", err)
	}
	for j := 0; j < config.NumField(); j++ {
		n, _ := fieldName(config.Field(j))
		if n == "command" || n == "_runtime" {
			return nil, fmt.Errorf("constructor field %q conflicts with Python runtime options", n)
		}
	}
	if t.NumOut() != 1 && t.NumOut() != 2 {
		return nil, fmt.Errorf("constructor must return *T or (*T, error)")
	}
	if t.NumOut() == 2 && t.Out(1) != errorType {
		return nil, fmt.Errorf("constructor second result must be error")
	}
	receiverType := t.Out(0)
	if receiverType.Kind() != reflect.Pointer || receiverType.Elem().Kind() != reflect.Struct || receiverType.Elem().Name() == "" {
		return nil, fmt.Errorf("constructor result must be a pointer to a named struct")
	}
	object := &Object{registry: r, receiverType: receiverType}
	r.constructor = &constructor{config: config, names: generated, initialize: func(ctx context.Context, raw json.RawMessage) error {
		v, err := decodeInput(raw, config)
		if err != nil {
			return Failure("invalid_argument", err.Error())
		}
		args := make([]reflect.Value, 0, t.NumIn())
		if offset == 1 {
			args = append(args, reflect.ValueOf(ctx))
		}
		args = append(args, v)
		results := f.Call(args)
		if t.NumOut() == 2 && !results[1].IsNil() {
			return results[1].Interface().(error)
		}
		if results[0].IsNil() {
			return Failure("internal", "constructor returned a nil receiver")
		}
		object.instance = results[0]
		return nil
	}}
	return object, nil
}

// Bind exposes a method expression such as (*Counter).Add. The receiver is
// supplied by the bridge; paramNames describe only the remaining wire arguments.
func (o *Object) Bind(name string, method any, paramNames ...string) error {
	if o == nil || o.registry == nil {
		return fmt.Errorf("object must not be nil")
	}
	if err := o.registry.checkName(name); err != nil {
		return err
	}
	f := reflect.ValueOf(method)
	if !f.IsValid() || f.Kind() != reflect.Func || f.IsNil() || f.Type().NumIn() == 0 || f.Type().In(0) != o.receiverType {
		return fmt.Errorf("method must be an expression with first parameter %s", o.receiverType)
	}
	op, err := compileBinding(name, f, func() reflect.Value { return o.instance }, paramNames)
	if err != nil {
		return fmt.Errorf("bind method %s: %w", name, err)
	}
	return o.registry.add(op)
}

// NeedsInit reports whether a declared constructor has not successfully run.
func (r *Registry) NeedsInit() bool {
	// Registration is complete before calls begin. Avoid a lock in the common
	// stateless function path, where there is no constructor state to publish.
	if r.constructor == nil {
		return false
	}
	r.initMu.Lock()
	defer r.initMu.Unlock()
	return r.constructor != nil && !r.initialized
}

// Initialize creates the process-owned receiver exactly once. Even failed
// attempts cannot be retried: constructors may have irreversible side effects.
// A new client process provides a fresh initialization attempt.
func (r *Registry) Initialize(ctx context.Context, config json.RawMessage) (err error) {
	var started time.Time
	if r.logger != nil || r.observer != nil {
		started = time.Now()
	}
	defer func() { r.observe(ctx, "$init", started, err) }()
	r.initMu.Lock()
	defer r.initMu.Unlock()
	defer func() {
		if recover() != nil {
			err = Failure("internal", "constructor panicked")
		}
	}()
	if r.constructor == nil {
		return Failure("failed_precondition", "no constructor is registered")
	}
	if r.initAttempt {
		return Failure("failed_precondition", "service initialization has already been attempted")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.initAttempt = true
	if err := r.constructor.initialize(ctx, config); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.initialized = true
	return nil
}

// schema gives reflection-created constructor groups stable public model names.
func (c *constructor) schema() Type {
	result := describe(c.config)
	var name func(*Type, reflect.Type)
	name = func(schema *Type, t reflect.Type) {
		if schema.Elem != nil {
			name(schema.Elem, t.Elem())
			return
		}
		if model, ok := c.names[t]; ok {
			schema.Name = model
		}
		for i := range schema.Fields {
			name(&schema.Fields[i].Type, t.Field(i).Type)
		}
	}
	name(&result, c.config)
	return result
}
