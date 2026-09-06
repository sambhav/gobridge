package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func validateCustomization(p project, python, typescript bool) error {
	requirements := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9_,.-]+\])?(\s*(~=|==|!=|<=|>=|<|>)\s*[A-Za-z0-9.*+!-]+(\s*,\s*(~=|==|!=|<=|>=|<|>)\s*[A-Za-z0-9.*+!-]+)*)?$`)
	if python {
		for _, requirement := range p.PythonRequires {
			if !requirements.MatchString(requirement) {
				return fmt.Errorf("invalid python.requires entry %q; use names, extras, and version comparisons", requirement)
			}
		}
	}
	if typescript {
		for name, value := range p.NPMDependencies {
			if !validNPMName(name) || strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\r\n\x00") {
				return fmt.Errorf("invalid typescript.dependencies entry %q", name)
			}
		}
	}
	roots := []string{}
	if python && p.PythonPackage != "" {
		roots = append(roots, p.PythonPackage)
	}
	if typescript && p.TypeScriptPackage != "" {
		roots = append(roots, p.TypeScriptPackage)
	}
	reserved := map[string]bool{"_bin": true, "_gobridge": true, "py.typed": true, "_bindings.py": true, "generated.ts": true, "package.json": true, "node_modules": true, "compiled": true, "tsconfig.json": true}
	for _, root := range roots {
		rel, err := filepath.Rel(absolute("."), absolute(root))
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("package source must be a subdirectory of the project: %s", root)
		}
		for parent := absolute(root); parent != absolute("."); parent = filepath.Dir(parent) {
			info, err := os.Lstat(parent)
			if err != nil {
				return err
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("package source must not traverse symlinks: %s", parent)
			}
		}
		err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path == root {
				return nil
			}
			if entry.Name() == "__pycache__" && entry.IsDir() {
				return filepath.SkipDir
			}
			if entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") || reserved[entry.Name()] || strings.HasPrefix(entry.Name(), "_gobridge_") {
				return fmt.Errorf("reserved or unsafe package addition: %s", path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// The public entrypoint commits an entire wrapper/bindings/runtime revision.
func buildDevPythonPackage(ctx context.Context, options devOptions, stage, binary, stem, suffix string, bindings []byte, log io.Writer) error {
	packageDir := filepath.Join(stage, "package")
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		return err
	}
	err := filepath.WalkDir(options.PythonPackage, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(options.PythonPackage, path)
		if err != nil {
			return err
		}
		if entry.Name() == "__pycache__" && entry.IsDir() {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".pyc") {
			return nil
		}
		dest := filepath.Join(packageDir, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0644)
	})
	if err != nil {
		return err
	}
	bindings, err = bundlePython(ctx, packageDir, bindings, log)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(packageDir, "_bindings.py"), bindings, 0644); err != nil {
		return err
	}
	if _, err = os.Stat(filepath.Join(packageDir, "__init__.py")); os.IsNotExist(err) {
		if err = os.WriteFile(filepath.Join(packageDir, "__init__.py"), []byte("from ._bindings import *\n"), 0644); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(filepath.Join(packageDir, "_bin"), 0755); err != nil {
		return err
	}
	if err = os.Rename(binary, filepath.Join(packageDir, "_bin", stem+suffix)); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(packageDir, "py.typed"), nil, 0644); err != nil {
		return err
	}
	hash := sha256.New()
	err = filepath.WalkDir(packageDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(packageDir, path)
		fmt.Fprintf(hash, "%s\x00%d\x00", rel, len(data))
		hash.Write(data)
		return nil
	})
	if err != nil {
		return err
	}
	revision := "_package_" + hex.EncodeToString(hash.Sum(nil))[:24]
	destination := filepath.Join(options.output, revision)
	if _, err = os.Stat(destination); os.IsNotExist(err) {
		if err = os.Rename(packageDir, destination); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(options.output, "py.typed"), nil); err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(options.output, "__init__.py"), []byte("from ."+revision+" import *\n")); err != nil {
		return err
	}
	_ = os.RemoveAll(filepath.Join(options.output, "__pycache__"))
	fmt.Fprintln(log, "Updated", options.output, "with", revision)
	return nil
}
