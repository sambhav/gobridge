package gobridge

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestAPIChanges(t *testing.T) {
	r := New()
	if err := Bind(r, "read_url", func(value uint64) uint64 { return value }, "value"); err != nil {
		t.Fatal(err)
	}
	before, err := r.API("Service")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Describe("read_url", "Updated docs"); err != nil {
		t.Fatal(err)
	}
	after, _ := r.API("Service")
	changes, err := DiffAPI(before, after)
	if err != nil || len(changes) != 2 {
		t.Fatal(changes, err)
	}
	for _, c := range changes {
		if c.Breaking {
			t.Fatal(c)
		}
	}
	if err = Bind(r, "added", func() {}); err != nil {
		t.Fatal(err)
	}
	after, _ = r.API("Service")
	changes, _ = DiffAPI(before, after)
	for _, c := range changes {
		if c.Breaking {
			t.Fatal(c)
		}
	}
	renamed := New(WithPython(Names{Operations: map[string]string{"read_url": "fetch"}}))
	if err = Bind(renamed, "read_url", func(value uint64) uint64 { return value }, "value"); err != nil {
		t.Fatal(err)
	}
	after, _ = renamed.API("Service")
	changes, _ = DiffAPI(before, after)
	found := false
	for _, c := range changes {
		if strings.Contains(c.Path, "public_name") && c.Breaking {
			found = true
		}
	}
	if !found {
		t.Fatal(changes)
	}
	data, _ := json.Marshal(before)
	var restored APISnapshot
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	changes, err = DiffAPI(before, restored)
	if err != nil || len(changes) != 0 {
		t.Fatal(changes, err)
	}
}

type invalidAdapter string

func (invalidAdapter) GobridgeWireType() reflect.Type { return reflect.TypeOf(invalidAdapter("")) }
func TestTypeValidation(t *testing.T) {
	r := New()
	if err := Bind(r, "invalid", func(invalidAdapter) {}, "value"); err == nil {
		t.Fatal("accepted recursive adapter")
	}
	if err := Bind(r, "array", func(value [2]uint16) [2]uint16 { return value }, "value"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"value":null}`, `{"value":[1]}`, `{"value":[1,2,3]}`, `{"value":[1,65536]}`} {
		if _, err := r.Call(context.Background(), "array", json.RawMessage(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	if _, err := r.Call(context.Background(), "array", json.RawMessage(`{"value":[0,65535]}`)); err != nil {
		t.Fatal(err)
	}
}

type cacheEnum string

func (cacheEnum) GobridgeEnum() map[string]cacheEnum { return map[string]cacheEnum{"One": "one"} }
func TestEnumSchemaSnapshotIsolation(t *testing.T) {
	r := New()
	if err := Bind(r, "enum", func(v cacheEnum) cacheEnum { return v }, "value"); err != nil {
		t.Fatal(err)
	}
	s := r.Schema()
	s.Operations[0].Output.Enum[0].Value[1] = 'x'
	if _, err := r.Call(context.Background(), "enum", json.RawMessage(`{"value":"one"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(context.Background(), "enum", json.RawMessage(`{"value":"xne"}`)); err == nil {
		t.Fatal("schema mutation changed validation")
	}
}
