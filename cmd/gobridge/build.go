package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sambhav/gobridge/internal/sourcegen"
)

// Build tooling comes from the same Go module version as the registry. Copy it
// into temporary staging so module caches stay read-only and outputs stay local.
func runBuild(ctx context.Context, args []string, log io.Writer) error {
	p, err := loadProject()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(log)
	python := flags.Bool("python", false, "build Python wheels (default if no language is selected)")
	typescript := flags.Bool("typescript", false, "build local npm tarballs with bundled binaries")
	output := flags.String("output", "dist", "artifact output directory")
	check := flags.Bool("check", false, "validate and print the build plan as JSON without writing files")
	dryRun := flags.Bool("dry-run", false, "alias for --check")
	replace := flags.Bool("replace", false, "replace existing artifacts with different contents")
	targets := flags.String("targets", "all", "all or comma-separated OS-architecture targets")
	flags.StringVar(&p.Source, "dir", p.Source, "annotated library directory; omit for manual registration")
	flags.StringVar(&p.Command, "command", p.Command, "Go executable package")
	flags.StringVar(&p.Name, "name", p.Name, "Python import path (dots allowed; binary uses underscores)")
	flags.StringVar(&p.Class, "class", p.Class, "generated client class")
	flags.StringVar(&p.Version, "version", p.Version, "application package version")
	flags.StringVar(&p.PythonDistribution, "distribution", p.PythonDistribution, "Python distribution name")
	flags.StringVar(&p.NPMPackage, "npm-package", p.NPMPackage, "npm package name")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected build arguments")
	}
	if err := p.validate(); err != nil {
		return err
	}
	if !*python && !*typescript {
		*python = true
	}
	if p.PythonDistribution == "" {
		p.PythonDistribution = p.distributionName()
	}
	if p.NPMPackage == "" {
		p.NPMPackage = p.distributionName()
	}
	plan, err := planBuild(ctx, p, *targets, *output, *python, *typescript)
	if err != nil {
		return err
	}
	if *check || *dryRun {
		return json.NewEncoder(os.Stdout).Encode(plan)
	}
	if p.Source != "" {
		if err := sourcegen.Generate(p.Source, "zz_gobridge.gen.go"); err != nil {
			return err
		}
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	source, err := moduleRoot(ctx, log)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "gobridge-build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	assets := []string{"LICENSE", "tools/package_customization.py", "tools/packaging_common.py"}
	if *python {
		assets = append(assets, "python/pyproject.toml", "python/src", "tools/build_wheels.py")
	}
	if *typescript {
		assets = append(assets, "typescript/src", "typescript/package.json", "typescript/package-lock.json", "tools/build_npm.py")
	}
	for _, path := range assets {
		if err := copyBuildAsset(source, stage, path); err != nil {
			return err
		}
	}
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		interpreter, err = exec.LookPath("python")
	}
	if err != nil {
		return fmt.Errorf("building packages requires Python 3.10+: %w", err)
	}
	execute := func(name string, args ...string) error {
		command := exec.CommandContext(ctx, name, args...)
		command.Dir = root
		command.Stdout = log
		command.Stderr = log
		return command.Run()
	}
	artifacts := filepath.Join(stage, "artifacts")
	common := []string{"--project", root, "--go-package", p.Command, "--class", p.Class, "--binary", p.binaryName(), "--version", p.Version, "--repository", p.Repository, "--license", p.License, "--build-cache", filepath.Join(stage, "go-build")}
	targetArgs := []string{}
	if *targets != "all" {
		targetArgs = append(targetArgs, "--targets")
		targetArgs = append(targetArgs, strings.Split(*targets, ",")...)
	}
	if *python {
		argv := append([]string{filepath.Join(stage, "tools/build_wheels.py")}, common...)
		argv = append(argv, "--package", p.Name, "--distribution", p.PythonDistribution, "--output", artifacts)
		argv = append(argv, targetArgs...)
		if err := execute(interpreter, argv...); err != nil {
			return fmt.Errorf("wheel build: %w", err)
		}
	}
	if *typescript {
		var install *exec.Cmd
		if runtime.GOOS == "windows" {
			install = exec.CommandContext(ctx, "cmd", "/d", "/c", "npm", "ci", "--ignore-scripts")
		} else {
			install = exec.CommandContext(ctx, "npm", "ci", "--ignore-scripts")
		}
		install.Dir = filepath.Join(stage, "typescript")
		install.Stdout = log
		install.Stderr = log
		if err := install.Run(); err != nil {
			return fmt.Errorf("Node 24+ and npm are required for TypeScript packaging: %w", err)
		}
		argv := append([]string{filepath.Join(stage, "tools/build_npm.py")}, common...)
		argv = append(argv, "--package", p.NPMPackage, "--output", filepath.Join(artifacts, "npm"))
		argv = append(argv, targetArgs...)
		if err := execute(interpreter, argv...); err != nil {
			return fmt.Errorf("npm package build: %w", err)
		}
	}
	if err := publishArtifacts(artifacts, absolute(*output), plan, *replace); err != nil {
		return err
	}
	fmt.Fprintln(log, "Packages built in", absolute(*output), "(nothing published)")
	return nil
}

func copyBuildAsset(source, destination, path string) error {
	return filepath.WalkDir(filepath.Join(source, filepath.FromSlash(path)), func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "__pycache__" {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		data, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}
