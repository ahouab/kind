/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package podman

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/version"

	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
)

// IsAvailable checks if podman is available in the system
func IsAvailable() bool {
	cmd := exec.Command("podman", "-v")
	lines, err := exec.OutputLines(cmd)
	if err != nil || len(lines) != 1 {
		return false
	}
	return strings.HasPrefix(lines[0], "podman version")
}

func getPodmanVersion() (*version.Version, error) {
	cmd := exec.Command("podman", "--version")
	lines, err := exec.OutputLines(cmd)
	if err != nil {
		return nil, err
	}

	// output is like `podman version 1.7.1-dev`
	if len(lines) != 1 {
		return nil, errors.Errorf("podman version should only be one line, got %d", len(lines))
	}
	parts := strings.Split(lines[0], " ")
	if len(parts) != 3 {
		return nil, errors.Errorf("podman --version contents should have 3 parts, got %q", lines[0])
	}
	return version.ParseSemantic(parts[2])
}

const (
	minSupportedVersion = "1.8.0"
)

func ensureMinVersion() error {
	// ensure that podman version is a compatible version
	v, err := getPodmanVersion()
	if err != nil {
		return errors.Wrap(err, "failed to check podman version")
	}
	if !v.AtLeast(version.MustParseSemantic(minSupportedVersion)) {
		return errors.Errorf("podman version %q is too old, please upgrade to %q or later", v, minSupportedVersion)
	}
	return nil
}

// createAnonymousVolume creates a new anonymous volume
// with the specified label=true
// returns the name of the volume created
func createAnonymousVolume(label string) (string, error) {
	cmd := exec.Command("podman",
		"volume",
		"create",
		// podman only support filter on key during list
		// so we use the unique id as key
		"--label", fmt.Sprintf("%s=true", label))
	name, err := exec.Output(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(name), "\n"), nil
}

// getVolumes gets volume names filtered on specified label
func getVolumes(label string) ([]string, error) {
	cmd := exec.Command("podman",
		"volume",
		"ls",
		"--filter", fmt.Sprintf("label=%s", label),
		"--quiet")
	// `output` from the above command is names of all volumes each followed by `\n`.
	output, err := exec.Output(cmd)
	if err != nil {
		return nil, err
	}
	// Trim away the last `\n`.
	trimmedOutput := strings.TrimSuffix(string(output), "\n")
	// Get names of all volumes by splitting via `\n`.
	return strings.Split(trimmedOutput, "\n"), nil
}

func deleteVolumes(names []string) error {
	args := []string{
		"volume",
		"rm",
		"--force",
	}
	args = append(args, names...)
	cmd := exec.Command("podman", args...)
	return cmd.Run()
}
