package main

import (
	"bytes"
	"encoding/json"
	bridge "github.com/sambhav/gobridge"
	"os"
	"path/filepath"
	"testing"
)

func TestAPIDiffCommand(t *testing.T) {
	r := bridge.New()
	before, _ := r.API("Service")
	if err := bridge.Bind(r, "added", func() {}); err != nil {
		t.Fatal(err)
	}
	after, _ := r.API("Service")
	dir := t.TempDir()
	old, new := filepath.Join(dir, "old.json"), filepath.Join(dir, "new.json")
	for path, value := range map[string]bridge.APISnapshot{old: before, new: after} {
		data, _ := json.Marshal(value)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := runAPIDiff([]string{"--check", old, new}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runAPIDiff([]string{"--check", new, old}, &out); err == nil {
		t.Fatal("removed API passed check")
	}
	if !json.Valid(bytes.Split(out.Bytes(), []byte("\n"))[0]) {
		t.Fatal(out.String())
	}
}
