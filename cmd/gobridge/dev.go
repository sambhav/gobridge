package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"time"

	"github.com/sambhav/gobridge/internal/sourcegen"
)

const devMarker = "gobridge development package v1\n"

type devOptions struct {
	project
	output   string
	app      []string
	interval time.Duration
	once     bool
}

func runDev(ctx context.Context, args []string, log io.Writer) error {
	p, err := loadProject()
	if err != nil {
		return err
	}
	options := devOptions{project: p}
	flags := flag.NewFlagSet("dev", flag.ContinueOnError)
	flags.SetOutput(log)
	flags.StringVar(&options.Source, "dir", p.Source, "annotated library directory; omit for manual registration")
	flags.StringVar(&options.Command, "command", p.Command, "Go executable package")
	flags.StringVar(&options.Name, "name", p.Name, "Python import and binary name")
	flags.StringVar(&options.Class, "class", p.Class, "generated client class")
	flags.StringVar(&options.output, "python", "", "generated Python package directory (default build/<name>)")
	flags.DurationVar(&options.interval, "interval", 500*time.Millisecond, "source polling interval")
	flags.BoolVar(&options.once, "once", false, "build one matching package and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	options.app = flags.Args()
	if err := options.validate(); err != nil {
		return err
	}
	if options.interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if options.once && len(options.app) > 0 {
		return fmt.Errorf("--once does not run an application; omit --once for reload")
	}
	if options.output == "" {
		options.output = filepath.Join("build", options.Name)
	}
	options.output = absolute(options.output)
	if filepath.Base(options.output) != options.Name {
		return fmt.Errorf("--python must end in the import package name %q", options.Name)
	}
	if err := prepareDevOutput(options.output); err != nil {
		return err
	}
	if options.once {
		return buildDev(ctx, options, log)
	}
	goHash, pyHash, err := sourceHashes(options.output)
	if err != nil {
		return err
	}
	var app *devApp
	ready := false
	defer func() {
		if app != nil {
			app.stop()
		}
	}()
	rebuild := func() bool {
		if err := buildDev(ctx, options, log); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(log, "Build failed; keeping the last working package:", err)
			}
			return false
		}
		ready = true
		return true
	}
	restart := func() {
		if !ready {
			return
		}
		if app != nil {
			app.stop()
			app = nil
		}
		if len(options.app) > 0 {
			var err error
			app, err = startDevApp(options.app, filepath.Dir(options.output))
			if err != nil {
				fmt.Fprintln(log, "Application:", err)
			}
		}
	}
	if rebuild() {
		restart()
	}
	fmt.Fprintln(log, "Watching Go and Python source; Ctrl-C stops development.")
	ticker := time.NewTicker(options.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		nextGo, nextPy, err := sourceHashes(options.output)
		if err != nil {
			fmt.Fprintln(log, "Watch:", err)
			continue
		}
		if nextGo != goHash {
			goHash, pyHash = nextGo, nextPy
			if rebuild() {
				restart()
			}
		} else if nextPy != pyHash {
			pyHash = nextPy
			restart()
		}
	}
}

func prepareDevOutput(output string) error {
	entries, err := os.ReadDir(output)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		marker, err := os.ReadFile(filepath.Join(output, ".gobridge-dev"))
		if err != nil || string(marker) != devMarker {
			return fmt.Errorf("refusing to overwrite an existing package: %s", output)
		}
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, ".gobridge-dev"), []byte(devMarker), 0644)
}

func buildDev(ctx context.Context, options devOptions, log io.Writer) error {
	if options.Source != "" {
		if err := sourcegen.Generate(options.Source, "zz_gobridge.gen.go"); err != nil {
			return err
		}
	}
	stage, err := os.MkdirTemp(options.output, ".build-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(stage, "service"+suffix)
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, options.Command)
	build.Stdout = log
	build.Stderr = log
	build.Env = hostGoEnv()
	if err := build.Run(); err != nil {
		return err
	}
	file, err := os.Open(binary)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	file.Close()
	if err != nil {
		return err
	}
	stem := options.Name + "-" + hex.EncodeToString(hash.Sum(nil))[:24]
	generate := exec.CommandContext(ctx, binary, "generate-python", "--class", options.Class, "--binary", stem)
	generate.Stderr = log
	bindings, err := generate.Output()
	if err != nil {
		return err
	}
	binaryDir := filepath.Join(options.output, "_bin")
	if err := os.MkdirAll(binaryDir, 0755); err != nil {
		return err
	}
	target := filepath.Join(binaryDir, stem+suffix)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.Chmod(binary, 0755); err != nil {
			return err
		}
		if err := os.Rename(binary, target); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(options.output, "py.typed"), nil); err != nil {
		return err
	}
	// The import entrypoint is the commit point; all referenced assets exist first.
	if err := atomicWrite(filepath.Join(options.output, "__init__.py"), bindings); err != nil {
		return err
	}
	// CPython timestamp caches can otherwise reuse old source after a fast edit.
	_ = os.RemoveAll(filepath.Join(options.output, "__pycache__"))
	fmt.Fprintln(log, "Updated", options.output, "with", stem)
	return nil
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Chmod(name, 0644); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func hostGoEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "GOOS" && key != "GOARCH" {
			env = append(env, entry)
		}
	}
	return env
}

func sourceHashes(output string) (string, string, error) {
	goHash, pyHash := sha256.New(), sha256.New()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules" || entry.Name() == "vendor" || entry.Name() == "__pycache__" || entry.Name() == "dist" || entry.Name() == "bin" || absolute(path) == output) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() == "zz_gobridge.gen.go" {
			return nil
		}
		var writer io.Writer
		switch {
		case strings.HasSuffix(path, ".go"), entry.Name() == "go.mod", entry.Name() == "go.sum", entry.Name() == "go.work":
			writer = goHash
		case strings.HasSuffix(path, ".py"):
			writer = pyHash
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintln(writer, path)
		_, err = writer.Write(data)
		return err
	})
	return hex.EncodeToString(goHash.Sum(nil)), hex.EncodeToString(pyHash.Sum(nil)), err
}

type devApp struct {
	command *exec.Cmd
	done    chan struct{}
}

func startDevApp(argv []string, packageParent string) (*devApp, error) {
	command := exec.Command(argv[0], argv[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "PYTHONPATH") {
			env = append(env, entry)
		}
	}
	path := packageParent
	if old := os.Getenv("PYTHONPATH"); old != "" {
		path += string(os.PathListSeparator) + old
	}
	command.Env = append(env, "PYTHONPATH="+path)
	if err := command.Start(); err != nil {
		return nil, err
	}
	app := &devApp{command: command, done: make(chan struct{})}
	go func() { _ = command.Wait(); close(app.done) }()
	return app, nil
}
func (app *devApp) stop() {
	select {
	case <-app.done:
		return
	default:
	}
	if err := app.command.Process.Signal(os.Interrupt); err != nil {
		_ = app.command.Process.Kill()
	}
	select {
	case <-app.done:
	case <-time.After(2 * time.Second):
		_ = app.command.Process.Kill()
		<-app.done
	}
}
