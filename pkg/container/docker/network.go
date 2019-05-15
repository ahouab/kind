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

package docker

import (
	"fmt"

	"sigs.k8s.io/kind/pkg/cluster/constants"
	"sigs.k8s.io/kind/pkg/exec"
)

// CreateNetwork create a bridge network
func CreateNetwork(networkName string) error {
	cmd := exec.Command(
		"docker", "network",
		"create",
		"--driver=bridge",
		"--label="+fmt.Sprintf("%s=%s", constants.ClusterLabelKey, networkName),
		networkName,
	)
	return cmd.Run()
}

// DeleteNetwork delete the special network
func DeleteNetwork(networkName string) error {
	cmd := exec.Command(
		"docker", "network",
		"rm",
		networkName,
	)
	return cmd.Run()
}

// IsNetworkExist check if the network exist
func IsNetworkExist(networkName string) bool {
	cmd := exec.Command(
		"docker", "network",
		"inspect",
		networkName,
	)
	if err := cmd.Run(); err != nil {
		return false
	}

	return true
}
