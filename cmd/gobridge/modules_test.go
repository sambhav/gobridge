package main

import (
	bridge "github.com/sambhav/gobridge"
	"testing"
)

func TestModuleOutputsAndClassDefaults(t *testing.T) {
	p := project{Version: "1.0.0", Modules: []module{{Name: "catalog", Command: "./cmd/catalog", Python: moduleTarget{Module: "acme.catalog"}, TypeScript: moduleTarget{Export: ".", Class: "CatalogApi"}}, {Name: "text", Command: "./cmd/text"}}}
	modules, err := p.resolveModules()
	if err != nil {
		t.Fatal(err)
	}
	if modules[0].Python.Class != "Catalog" || modules[0].Python.Rename.Class != "" {
		t.Fatal("default class should not override annotations")
	}
	if modules[0].TypeScript.Rename.Class != "CatalogApi" || modules[1].TypeScript.Export != "./text" {
		t.Fatalf("unexpected modules: %+v", modules)
	}
}
func TestModuleOutputValidation(t *testing.T) {
	for _, modules := range [][]module{
		{{Name: "a", Command: "."}, {Name: "a", Command: "."}},
		{{Name: "a", Command: ".", Python: moduleTarget{Module: "acme.shared"}}, {Name: "b", Command: ".", Python: moduleTarget{Module: "acme.shared"}}},
		{{Name: "a", Command: ".", TypeScript: moduleTarget{Export: "./shared"}}, {Name: "b", Command: ".", TypeScript: moduleTarget{Export: "./shared"}}},
		{{Name: "a", Command: ".", TypeScript: moduleTarget{Export: "../outside"}}},
		{{Name: "a", Command: ".", Python: moduleTarget{Module: "gobridge.internal"}}},
		{{Name: "a", Command: ".", Python: moduleTarget{Rename: bridge.Names{Class: "Misplaced"}}}},
	} {
		if _, err := (project{Version: "1.0.0", Modules: modules}).resolveModules(); err == nil {
			t.Fatalf("accepted %+v", modules)
		}
	}
}
