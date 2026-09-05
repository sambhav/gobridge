package gobridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"
)

type fuzzChild struct {
	Name string `json:"name"`
}
type fuzzInput struct {
	Text  string      `json:"text"`
	Count int64       `json:"count"`
	Child *fuzzChild  `json:"child,omitempty"`
	Items []fuzzChild `json:"items"`
	Data  []byte      `json:"data"`
	At    time.Time   `json:"at"`
}

// Reference implementation protects the strict decode contract while optimizing
// its allocation cost: unknown/missing fields, numeric ranges, trailing JSON,
// nullable containers, custom codecs, and duplicate/case-variant fields.
func legacyDecode(raw []byte, typ reflect.Type) (reflect.Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = []byte("{}")
	}
	if err := validateValue(raw, typ); err != nil {
		return reflect.Value{}, err
	}
	v := reflect.New(typ)
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v.Interface()); err != nil {
		return reflect.Value{}, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return reflect.Value{}, fmt.Errorf("expected one object")
	}
	return v.Elem(), nil
}

func FuzzDecodeInput(f *testing.F) {
	for _, seed := range []string{
		`{"text":"hello","count":1,"items":[],"data":"AAH/","at":"2026-09-05T00:00:00.123456789Z"}`,
		`{"text":"first","text":"last","count":9223372036854775807,"child":null,"items":null,"data":null,"at":"2026-09-05T00:00:00Z"}`,
		`{"Text":"wrong case"}`, `{}`, `null`, `[]`, `{} {}`, "\xff", `{"count":9223372036854775808}`,
	} {
		f.Add([]byte(seed))
	}
	typ := reflect.TypeOf(fuzzInput{})
	if err := validateType(typ, map[reflect.Type]bool{}); err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 65536 {
			t.Skip()
		}
		want, wantErr := legacyDecode(raw, typ)
		got, gotErr := decodeInput(raw, typ)
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("acceptance differs for %q: old=%v new=%v", raw, wantErr, gotErr)
		}
		if wantErr == nil && !reflect.DeepEqual(want.Interface(), got.Interface()) {
			t.Fatalf("decoded value differs for %q", raw)
		}
	})
}

func FuzzProtocolFrames(f *testing.F) {
	for _, seed := range []string{
		"{\"id\":\"1\",\"method\":\"$hello\"}\n",
		"{\"id\":\"1\",\"method\":\"echo\",\"params\":{\"text\":\"ok\"}}\n",
		"{\"method\":\"$cancel\",\"params\":{\"id\":\"1\"}}\n",
		"{\"id\":\"1\"}\n{\"id\":\"1\"}\n", "{", "null\n", "\xff\n",
	} {
		f.Add([]byte(seed))
	}
	r := New()
	if err := Bind(r, "echo", func(text string) string { return text }, "text"); err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 4096 {
			t.Skip()
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = r.Serve(ctx, bytes.NewReader(raw), io.Discard, 8)
	})
}
