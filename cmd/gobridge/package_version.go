package main

import (
	"fmt"
	"regexp"
	"strconv"
)

// Keep the canonical grammar in sync with tools/packaging_common.py.
var packageVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)

// pythonPackageVersion validates the shared npm-style input and maps it to PEP 440.
func pythonPackageVersion(version string) (string, error) {
	parts := packageVersionPattern.FindStringSubmatch(version)
	if parts == nil {
		return "", fmt.Errorf("version must be X.Y.Z or X.Y.Z-{alpha,beta,rc}.N (for example 1.2.3-rc.1); no leading zeros or build metadata")
	}
	for _, index := range []int{1, 2, 3, 5} {
		if parts[index] == "" {
			continue
		}
		n, err := strconv.ParseUint(parts[index], 10, 64)
		if err != nil || n > 9007199254740991 {
			return "", fmt.Errorf("version components must be at most 9007199254740991 (npm's safe integer limit)")
		}
	}
	result := parts[1] + "." + parts[2] + "." + parts[3]
	if parts[4] != "" {
		result += map[string]string{"alpha": "a", "beta": "b", "rc": "rc"}[parts[4]] + parts[5]
	}
	return result, nil
}
