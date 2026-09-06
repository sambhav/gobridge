package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPackageVersions(t *testing.T) {
	data, err := os.ReadFile("../../testdata/package_versions.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Valid   map[string]string
		Invalid []string
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for input, want := range cases.Valid {
		got, err := pythonPackageVersion(input)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range cases.Invalid {
		if _, err := pythonPackageVersion(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
