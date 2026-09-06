package main

import (
	"strings"
	"testing"
)

func TestDiscoverSourceProject(t *testing.T) {
	inDirectory(t, t.TempDir())
	writeTestFile(t, "go.mod", "module example.test/sdk\n\ngo 1.23\n")
	writeTestFile(t, "bridge/doc.go", `// Package bridge is the API.
//gobridge:module acme.greeter
//gobridge:version 1.2.3
//gobridge:npm @acme/sdk
package bridge
`)
	p, err := loadProject()
	if err != nil {
		t.Fatal(err)
	}
	modules, err := p.resolveModules()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "acme.greeter" || p.Version != "1.2.3" || p.NPMPackage != "@acme/sdk" || len(modules) != 1 || modules[0].Source != "./bridge" || modules[0].Command != "./cmd/bridge" || modules[0].TypeScript.Export != "." {
		t.Fatalf("bad defaults: %+v %+v", p, modules)
	}
	for _, path := range []string{"build/generated.go", "node_modules/dep/doc.go", "vendor/dep/doc.go", "nested/doc.go", "bridge/ignored_test.go"} {
		writeTestFile(t, path, "//gobridge:module should.not.appear\npackage ignored\n")
	}
	writeTestFile(t, "nested/go.mod", "module example.test/other\n")
	writeTestFile(t, "bridge/excluded.go", "//go:build ignore\n\n//gobridge:module excluded\npackage bridge\n")
	writeTestFile(t, "other/doc.go", `//gobridge:module acme.catalog
//gobridge:command ./cmd/host
//gobridge:command-prefix ["catalog", "bridge"]
//gobridge:ts-export ./catalog
package catalog
`)
	p, err = loadProject()
	if err != nil {
		t.Fatal(err)
	}
	modules, err = p.resolveModules()
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 || modules[1].Command != "./cmd/host" || strings.Join(modules[1].CommandPrefix, "/") != "catalog/bridge" || modules[1].TypeScript.Export != "./catalog" {
		t.Fatalf("bad discovered modules: %+v", modules)
	}
	// An explicit manifest selects its own modules; discovery does not combine
	// unrelated APIs with an intentionally configured package.
	writeTestFile(t, "gobridge.json", `{"name":"manual","command":"."}`)
	p, err = loadProject()
	if err != nil || len(p.Modules) != 1 || p.Name != "manual" || p.Modules[0].Source != "" {
		t.Fatalf("manifest selection: %+v %v", p, err)
	}
}

func TestDiscoverRejectsAmbiguousSettings(t *testing.T) {
	for _, tc := range []struct{ comments, want string }{
		{"//gobridge:module\n", "needs a value"},
		{"//gobridge:modul typo\n", "require //gobridge:module"},
		{"//gobridge:module api\n//gobridge:unknown x\n", "unknown package setting"},
		{"//gobridge:module api\n//gobridge:module other\n", "duplicate"},
		{"//gobridge:module api\n//gobridge:command-prefix shell command\n", "JSON string array"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			inDirectory(t, t.TempDir())
			writeTestFile(t, "doc.go", tc.comments+"package main\n")
			if _, err := loadProject(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

func TestFlatProjectDefaults(t *testing.T) {
	inDirectory(t, t.TempDir())
	writeTestFile(t, "gobridge.json", `{"name":"acme.greeter"}`)
	p, err := loadProject()
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "0.1.0" || p.Modules[0].Source != "./bridge" || p.Modules[0].Command != "./cmd/bridge" || p.Modules[0].TypeScript.Export != "." {
		t.Fatalf("bad defaults: %+v", p)
	}
}
