package gobridge

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
)

// WireTyper opts a custom JSON/text type into a supported wire representation.
// The method must work on a zero value, be deterministic and side-effect-free.
// MarshalJSON/UnmarshalJSON (or text methods) remain responsible for conversion.
type WireTyper interface{ GobridgeWireType() reflect.Type }

type wireAdapter struct {
	typ reflect.Type
	err error
}

var wireAdapters sync.Map

func adaptedType(t reflect.Type) (reflect.Type, error) {
	if t.Name() == "" || t.PkgPath() == "" {
		return nil, nil
	}
	if cached, ok := wireAdapters.Load(t); ok {
		a := cached.(wireAdapter)
		return a.typ, a.err
	}
	a := wireAdapter{}
	func() {
		defer func() {
			if recover() != nil {
				a.err = fmt.Errorf("%s: GobridgeWireType panicked", t)
			}
		}()
		if value, ok := reflect.New(t).Interface().(WireTyper); ok {
			a.typ = value.GobridgeWireType()
			if a.typ == nil || a.typ == t {
				a.err = fmt.Errorf("%s: adapter must name a different, non-nil wire type", t)
			}
		}
	}()
	wireAdapters.Store(t, a)
	return a.typ, a.err
}

// EnumValue records a public constant and its exact JSON scalar value.
type EnumValue struct {
	Name  string          `json:"name"`
	Value json.RawMessage `json:"value"`
}
type enumInfo struct {
	values []EnumValue
	err    error
}

var enumCache sync.Map

// A named scalar may declare GobridgeEnum() map[string]T. Source generation
// supplies this method for //gobridge:enum types with explicitly typed constants.
func enumValues(t reflect.Type) ([]EnumValue, error) {
	if t.Name() == "" || t.PkgPath() == "" {
		return nil, nil
	}
	if cached, ok := enumCache.Load(t); ok {
		e := cached.(enumInfo)
		return e.values, e.err
	}
	e := enumInfo{}
	method := reflect.New(t).MethodByName("GobridgeEnum")
	if method.IsValid() {
		func() {
			defer func() {
				if recover() != nil {
					e.err = fmt.Errorf("%s: GobridgeEnum panicked", t)
				}
			}()
			mt := method.Type()
			if mt.NumIn() != 0 || mt.NumOut() != 1 || mt.Out(0).Kind() != reflect.Map || mt.Out(0).Key().Kind() != reflect.String || mt.Out(0).Elem() != t {
				e.err = fmt.Errorf("%s: GobridgeEnum must return map[string]%s", t, t)
				return
			}
			kind := t.Kind()
			if kind != reflect.String && !(kind >= reflect.Int && kind <= reflect.Uint64) {
				e.err = fmt.Errorf("enum %s must be a string or integer", t)
				return
			}
			values := method.Call(nil)[0]
			for _, key := range values.MapKeys() {
				if !typescriptIdentifier.MatchString(key.String()) || key.String() == "" || key.String() == "__proto__" || key.String() == "prototype" || key.String() == "constructor" {
					e.err = fmt.Errorf("invalid enum constant %q", key.String())
					return
				}
				raw, err := json.Marshal(values.MapIndex(key).Interface())
				if err != nil {
					e.err = err
					return
				}
				e.values = append(e.values, EnumValue{key.String(), raw})
			}
			sort.Slice(e.values, func(i, j int) bool { return e.values[i].Name < e.values[j].Name })
			if len(e.values) == 0 {
				e.err = fmt.Errorf("enum %s requires values", t)
			}
		}()
	}
	enumCache.Store(t, e)
	return e.values, e.err
}
