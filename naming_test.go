package gobridge

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

type namedRecord struct {
	Value string `json:"value" python:"text" ts:"textValue"`
}

func TestNamesPreserveWireSchemaAndCompose(t *testing.T) {
	names := Names{Operations: map[string]string{"echo": "repeat"}, Types: map[string]string{"namedRecord": "RecordValue"}}
	option := WithPython(names)
	names.Operations["echo"] = "changed"
	r := New(option)
	if err := Bind(r, "echo", func(value namedRecord) namedRecord { return value }, "value"); err != nil {
		t.Fatal(err)
	}
	original := r.Schema().Hash
	var out bytes.Buffer
	if err := r.GeneratePython(&out, "Example_Client", "example", WithPython(Names{Fields: map[string]string{"namedRecord.value": "content"}})); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"def repeat(", "class RecordValue:", "content: str", `"wire_name":"value"`, `self.call("echo"`} {
		if !strings.Contains(out.String(), expected) {
			t.Errorf("missing %s", expected)
		}
	}
	if r.Schema().Hash != original {
		t.Fatal("renames changed wire schema")
	}
	out.Reset()
	if err := r.GeneratePython(&out, "Example", "example"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "text: str") {
		t.Fatal("per-generation option leaked")
	}
}
func TestNamesRejectTyposAndCollisions(t *testing.T) {
	for _, names := range []Names{{Operations: map[string]string{"missing": "name"}}, {Types: map[string]string{"missing": "Name"}}, {Fields: map[string]string{"namedRecord.nope": "name"}}, {Operations: map[string]string{"echo": "close"}}, {Fields: map[string]string{"namedRecord.value": "self"}}} {
		r := New()
		_ = Bind(r, "echo", func(value namedRecord) namedRecord { return value }, "value")
		if err := r.GeneratePython(io.Discard, "Example", "example", WithPython(names)); err == nil {
			t.Fatalf("accepted %+v", names)
		}
	}
}
