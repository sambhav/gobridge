package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Stage on the destination filesystem and publish the completion manifest last.
// A lock excludes cooperating builders; backups allow rollback on commit errors.
func publishArtifacts(source, output string, plan buildPlan, replace bool) error {
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	lock := filepath.Join(output, ".gobridge-build-lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("build output is locked (%s): %w", lock, err)
	}
	f.Close()
	defer os.Remove(lock)
	stage, err := os.MkdirTemp(output, ".publish-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	type artifact struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Size   int    `json:"size"`
	}
	manifest := struct {
		Plan      buildPlan  `json:"plan"`
		Artifacts []artifact `json:"artifacts"`
	}{Plan: plan}
	var paths []string
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("artifact must be a regular file: %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dest := filepath.Join(output, rel)
		for parent := filepath.Dir(dest); parent != output; parent = filepath.Dir(parent) {
			if info, e := os.Lstat(parent); e == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return fmt.Errorf("invalid output directory %s", parent)
			}
		}
		if info, e := os.Lstat(dest); e == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("artifact destination is not a regular file: %s", dest)
			}
			old, e := os.ReadFile(dest)
			if e != nil {
				return e
			}
			if !replace && !bytes.Equal(old, data) {
				return fmt.Errorf("artifact already exists with different contents: %s (use --replace)", dest)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		staged := filepath.Join(stage, "new", rel)
		if err := os.MkdirAll(filepath.Dir(staged), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(staged, data, 0644); err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		manifest.Artifacts = append(manifest.Artifacts, artifact{filepath.ToSlash(rel), hex.EncodeToString(digest[:]), len(data)})
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	const manifestName = "gobridge-build.json"
	if err := os.WriteFile(filepath.Join(stage, "new", manifestName), append(data, '\n'), 0644); err != nil {
		return err
	}
	paths = append(paths, manifestName)
	// Invalidate the previous completion marker before making any artifacts visible.
	var committed, backed []string
	rollback := func() {
		for i := len(committed) - 1; i >= 0; i-- {
			_ = os.Remove(filepath.Join(output, committed[i]))
		}
		for i := len(backed) - 1; i >= 0; i-- {
			_ = os.Rename(filepath.Join(stage, "old", backed[i]), filepath.Join(output, backed[i]))
		}
	}
	backup := func(rel string) error {
		dest := filepath.Join(output, rel)
		if info, e := os.Lstat(dest); e == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("destination must be a regular file: %s", dest)
			}
			old := filepath.Join(stage, "old", rel)
			if e = os.MkdirAll(filepath.Dir(old), 0755); e != nil {
				return e
			}
			data, e := os.ReadFile(dest)
			if e != nil {
				return e
			}
			if e = os.WriteFile(old, data, 0644); e != nil {
				return e
			}
			backed = append(backed, rel)
		} else if !os.IsNotExist(e) {
			return e
		}
		return nil
	}
	if err := backup(manifestName); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(output, manifestName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, rel := range paths {
		if rel != manifestName {
			if err := backup(rel); err != nil {
				rollback()
				return err
			}
		}
		dest := filepath.Join(output, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			rollback()
			return err
		}
		if err := os.Rename(filepath.Join(stage, "new", rel), dest); err != nil {
			rollback()
			return err
		}
		committed = append(committed, rel)
	}
	return nil
}
