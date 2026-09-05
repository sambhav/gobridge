// Package gobridge exposes typed Go operations through a CLI and a local
// multiplexed stdio protocol. Register operations before serving; handlers
// run concurrently and must respect context cancellation.
package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"sync"
)

// Error is a stable error code and a user-facing message, preserved in Python.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string           { return e.Code + ": " + e.Message }
func Failure(code, message string) error { return &Error{code, message} }
func wireError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{"deadline_exceeded", "request deadline exceeded"}
	}
	if errors.Is(err, context.Canceled) {
		return &Error{"cancelled", "request cancelled"}
	}
	return &Error{"internal", err.Error()}
}

type operation struct {
	name, description string
	in, out           reflect.Type
	inName            string
	call              func(context.Context, json.RawMessage) (any, error)
}

func (op operation) inputSchema() Type {
	t := describe(op.in)
	if op.inName != "" {
		t.Name = op.inName
	}
	return t
}

// Registry is immutable once serving starts. Registration is not concurrent.
type Registry struct {
	ops         map[string]operation
	constructor *constructor
	initMu      sync.Mutex
	initAttempt bool
	initialized bool
}

func New() *Registry { return &Registry{ops: make(map[string]operation)} }

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Register adapts an ordinary typed Go function. Both wire types must be named
// structs using the supported JSON subset; schema errors fail at registration.
func Register[I, O any](r *Registry, name, description string, fn func(context.Context, I) (O, error)) error {
	if err := r.checkName(name); err != nil {
		return err
	}
	if fn == nil {
		return fmt.Errorf("operation function must not be nil")
	}
	i, o := reflect.TypeOf((*I)(nil)).Elem(), reflect.TypeOf((*O)(nil)).Elem()
	for _, t := range []reflect.Type{i, o} {
		if t.Kind() != reflect.Struct || t.Name() == "" {
			return fmt.Errorf("operation types must be named structs")
		}
		if err := validateType(t, make(map[reflect.Type]bool)); err != nil {
			return err
		}
	}
	return r.add(operation{name: name, description: description, in: i, out: o, call: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var v I
		if len(raw) == 0 {
			raw = []byte("{}")
		}
		if err := validateValue(raw, i); err != nil {
			return nil, Failure("invalid_argument", err.Error())
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(&v); err != nil {
			return nil, Failure("invalid_argument", err.Error())
		}
		if err := d.Decode(new(any)); err != io.EOF {
			return nil, Failure("invalid_argument", "expected a single JSON object")
		}
		return fn(ctx, v)
	}})
}

func (r *Registry) checkName(name string) error {
	if r == nil {
		return fmt.Errorf("registry must not be nil")
	}
	if !identifier.MatchString(name) || pythonReserved[name] {
		return fmt.Errorf("invalid or reserved operation name %q", name)
	}
	if _, exists := r.ops[name]; exists {
		return fmt.Errorf("duplicate operation %q", name)
	}
	return nil
}

func (r *Registry) add(op operation) error {
	if err := r.checkName(op.name); err != nil {
		return err
	}
	if r.ops == nil {
		r.ops = make(map[string]operation)
	}
	r.ops[op.name] = op
	return nil
}

// Describe supplies help text for an already registered function or method.
func (r *Registry) Describe(name, description string) error {
	if r == nil {
		return fmt.Errorf("registry must not be nil")
	}
	op, ok := r.ops[name]
	if !ok {
		return fmt.Errorf("unknown operation %q", name)
	}
	op.description = description
	r.ops[name] = op
	return nil
}
func (r *Registry) names() []string {
	names := make([]string, 0, len(r.ops))
	for n := range r.ops {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (r *Registry) Call(ctx context.Context, name string, params json.RawMessage) (result any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = Failure("internal", "operation panicked")
		}
	}()
	op, ok := r.ops[name]
	if !ok {
		return nil, Failure("not_found", "unknown operation: "+name)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if r.NeedsInit() {
		return nil, Failure("failed_precondition", "initialize the service before calling operations")
	}
	// Initialization may have held initMu while this request's deadline expired.
	// Do not invoke a context-free function after waiting for that initialization.
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	result, err = op.call(ctx, params)
	if err == nil {
		err = ctx.Err()
	}
	return
}
