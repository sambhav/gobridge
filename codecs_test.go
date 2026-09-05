package gobridge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeCodecSchemaAndValidation(t *testing.T) {
	for _, item := range []struct {
		value          any
		kind           string
		valid, invalid string
	}{
		{[]byte(nil), "bytes", `"AAH/"`, `"not base64"`},
		{time.Time{}, "timestamp", `"2026-09-05T12:34:56.123456789+02:00"`, `"2026-99-99"`},
		{time.Duration(0), "duration", `9223372036854775807`, `9223372036854775808`},
	} {
		typ := reflect.TypeOf(item.value)
		if err := validateType(typ, map[reflect.Type]bool{}); err != nil {
			t.Fatal(err)
		}
		if got := describe(typ); got.Kind != item.kind {
			t.Fatalf("schema: %+v", got)
		}
		if err := validateValue(json.RawMessage(item.valid), typ); err != nil {
			t.Fatal(err)
		}
		err := validateValue(json.RawMessage(item.invalid), typ)
		if err == nil {
			err = json.Unmarshal([]byte(item.invalid), reflect.New(typ).Interface())
		}
		if err == nil {
			t.Fatalf("accepted %s for %s", item.invalid, item.kind)
		}
	}
	if err := validateValue(json.RawMessage("null"), reflect.TypeOf(time.Time{})); err == nil {
		t.Fatal("accepted null timestamp")
	}
	if err := validateValue(json.RawMessage("null"), reflect.TypeOf([]byte(nil))); err != nil {
		t.Fatal(err)
	}
	r := New()
	if err := Bind(r, "echo", func(data []byte, at time.Time, delay time.Duration) []byte { return data }, "data", "at", "delay"); err != nil {
		t.Fatal(err)
	}
	var py, ts strings.Builder
	if err := r.GeneratePython(&py, "Native", "native"); err != nil {
		t.Fatal(err)
	}
	if err := r.GenerateTypeScript(&ts, "Native", "native"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(py.String(), "data: bytes | None") || !strings.Contains(ts.String(), "Uint8Array | null") {
		t.Fatal("missing native byte annotations")
	}
}
