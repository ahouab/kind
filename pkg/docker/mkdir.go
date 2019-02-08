/*
Copyright 2018 The Kubernetes Authors.

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

package docker

import (
	"sigs.k8s.io/kind/pkg/exec"
)

// MkDirPath makes the directory path in the container
func MkDirPath(dirPath, containerNameOrID string) error {
	cmd := exec.Command(
		"docker", "exec",
		containerNameOrID,
		"mkdir", "-p",
		dirPath,
	)
	return cmd.Run()
}
