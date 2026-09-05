package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var supportedTargets = []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"}

type buildPlan struct {
	Project    project           `json:"project"`
	Targets    []string          `json:"targets"`
	Python     bool              `json:"python"`
	TypeScript bool              `json:"typescript"`
	Output     string            `json:"output"`
	Tools      map[string]string `json:"tools"`
}

func planBuild(ctx context.Context, p project, targets, output string, python, typescript bool) (buildPlan, error) {
	plan := buildPlan{Project: p, Python: python, TypeScript: typescript, Output: absolute(output), Tools: map[string]string{}}
	if err := p.validate(); err != nil {
		return plan, err
	}
	if strings.TrimSpace(output) == "" {
		return plan, fmt.Errorf("output must not be empty")
	}
	if python && (!regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`).MatchString(p.PythonDistribution) || regexp.MustCompile(`[-_.]+`).ReplaceAllString(strings.ToLower(p.PythonDistribution), "-") == "gobridge-runtime") {
		return plan, fmt.Errorf("invalid Python distribution %q", p.PythonDistribution)
	}
	if typescript && (len(p.NPMPackage) > 214 || !regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`).MatchString(p.NPMPackage) || p.NPMPackage == "gobridge-runtime") {
		return plan, fmt.Errorf("invalid npm package %q", p.NPMPackage)
	}
	for _, value := range []string{p.Repository, p.License} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return plan, fmt.Errorf("package metadata must not contain newlines or NUL")
		}
	}
	selected := strings.Split(targets, ",")
	if targets == "all" {
		selected = supportedTargets
	}
	seen := map[string]bool{}
	for _, target := range selected {
		valid := false
		for _, candidate := range supportedTargets {
			valid = valid || target == candidate
		}
		if !valid || seen[target] {
			return plan, fmt.Errorf("invalid or duplicate target %q; supported: %s", target, strings.Join(supportedTargets, ","))
		}
		seen[target] = true
		plan.Targets = append(plan.Targets, target)
	}
	for path := plan.Output; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return plan, fmt.Errorf("output ancestor must be a directory, not a symlink: %s", path)
			}
			break
		}
		if !os.IsNotExist(err) {
			return plan, err
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	if p.Source != "" {
		if info, err := os.Stat(p.Source); err != nil || !info.IsDir() {
			return plan, fmt.Errorf("source directory does not exist: %s", p.Source)
		}
	}
	checks := []struct {
		name string
		args []string
	}{{"go", []string{"version"}}, {"python3", []string{"-c", "import sys; assert sys.version_info >= (3,10), 'Python 3.10+ required'; print(sys.version.split()[0])"}}}
	if typescript {
		checks = append(checks, struct {
			name string
			args []string
		}{"node", []string{"-e", "if(Number(process.versions.node.split('.')[0])<24)process.exit(1);console.log(process.versions.node)"}}, struct {
			name string
			args []string
		}{"npm", []string{"--version"}})
	}
	for _, check := range checks {
		name, err := exec.LookPath(check.name)
		if err != nil && check.name == "python3" {
			name, err = exec.LookPath("python")
		}
		if err != nil {
			return plan, fmt.Errorf("required tool %s: %w", check.name, err)
		}
		cmd := exec.CommandContext(ctx, name, check.args...)
		data, err := cmd.CombinedOutput()
		if err != nil {
			return plan, fmt.Errorf("check %s: %w: %s", check.name, err, data)
		}
		plan.Tools[check.name] = strings.TrimSpace(string(data))
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-json", p.Command)
	data, err := cmd.Output()
	if err != nil {
		return plan, fmt.Errorf("inspect Go command %q: %w", p.Command, err)
	}
	var pkg struct {
		Name string
		Dir  string
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Name != "main" || pkg.Dir == "" {
		return plan, fmt.Errorf("command %q must resolve to a Go main package", p.Command)
	}
	return plan, nil
}
