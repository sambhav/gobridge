package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func moduleRoot(ctx context.Context, log io.Writer) (string, error) {
	lookup := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", "github.com/sambhav/gobridge")
	lookup.Stderr = log
	location, err := lookup.Output()
	if err != nil {
		return "", fmt.Errorf("locate gobridge sources in this Go module: %w", err)
	}
	root := strings.TrimSpace(string(location))
	if root == "" {
		return "", fmt.Errorf("gobridge module source is unavailable")
	}
	return root, nil
}

// Publish immutable, private runtime revisions just like the paired Go binary.
// Old imports retain their own implementation when go.mod changes in dev mode.
func bundlePython(ctx context.Context, output string, bindings []byte, log io.Writer) ([]byte, error) {
	root, err := moduleRoot(ctx, log)
	if err != nil {
		return nil, err
	}
	source := filepath.Join(root, "python", "src", "gobridge")
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	type asset struct {
		name string
		data []byte
	}
	var assets []asset
	hash := sha256.New()
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".py") || entry.Name() == "py.typed") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			return nil, err
		}
		assets = append(assets, asset{entry.Name(), data})
		fmt.Fprintf(hash, "%s\x00%d\x00", entry.Name(), len(data))
		hash.Write(data)
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		return nil, err
	}
	assets = append(assets, asset{"LICENSE", license})
	hash.Write(license)
	name := "_gobridge_" + hex.EncodeToString(hash.Sum(nil))[:24]
	target := filepath.Join(output, name)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		stage, err := os.MkdirTemp(output, ".runtime-")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		for _, file := range assets {
			if err := os.WriteFile(filepath.Join(stage, file.name), file.data, 0644); err != nil {
				return nil, err
			}
		}
		if err := os.Rename(stage, target); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(string(bindings), "\nfrom gobridge", "\nfrom ."+name)), nil
}
