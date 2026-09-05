package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func buildDevTypeScript(ctx context.Context, options devOptions, binary string, log io.Writer) error {
	root, err := moduleRoot(ctx, log)
	if err != nil {
		return err
	}
	// Dependencies belong to this development package, never the Go module cache.
	tooling := filepath.Join(options.output, ".tooling")
	for _, path := range []string{"typescript/src", "typescript/package.json", "typescript/package-lock.json", "tools/build_npm.py", "tools/package_customization.py", "LICENSE"} {
		if err := copyBuildAsset(root, tooling, path); err != nil {
			return err
		}
	}
	compiler := filepath.Join(tooling, "typescript", "node_modules", "typescript", "bin", "tsc")
	lock, err := os.ReadFile(filepath.Join(tooling, "typescript", "package-lock.json"))
	if err != nil {
		return err
	}
	installed, _ := os.ReadFile(filepath.Join(tooling, "typescript", ".installed-lock"))
	if _, err := os.Stat(compiler); os.IsNotExist(err) || !bytes.Equal(lock, installed) {
		name := "npm"
		args := []string{"ci", "--ignore-scripts"}
		if runtime.GOOS == "windows" {
			name = "cmd"
			args = append([]string{"/d", "/c", "npm"}, args...)
		}
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = filepath.Join(tooling, "typescript")
		cmd.Stdout = log
		cmd.Stderr = log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("install TypeScript compiler: %w", err)
		}
		if err := os.WriteFile(filepath.Join(tooling, "typescript", ".installed-lock"), lock, 0644); err != nil {
			return err
		}
	}
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		interpreter, err = exec.LookPath("python")
	}
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, interpreter, filepath.Join(tooling, "tools", "build_npm.py"), "--project", absolute("."), "--package", options.NPMPackage, "--class", options.Class, "--binary", options.binaryName(), "--version", options.Version, "--targets", runtime.GOOS+"-"+runtime.GOARCH, "--host-binary", binary, "--dev-output", options.output)
	cmd.Stdout = log
	cmd.Stderr = log
	return cmd.Run()
}
