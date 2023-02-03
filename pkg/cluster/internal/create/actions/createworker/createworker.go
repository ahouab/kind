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

// Package createworker implements the create worker action
package createworker

import (
	"bytes"
	"fmt"
	"os"

	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions/cluster"
	"sigs.k8s.io/kind/pkg/errors"
)

type action struct {
	vaultPassword  string
	descriptorName string
}

// SecretsFile represents the YAML structure in the secrets.yml file
type SecretsFile struct {
	Secrets struct {
		AWS struct {
			Credentials `yaml:"credentials"`
		} `yaml:"aws"`
		GCP struct {
			Credentials `yaml:"credentials"`
		} `yaml:"gcp"`
		GithubToken string `yaml:"github_token"`
	}
}

const allowAllEgressNetPol = `
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-all-egress
spec:
  egress:
  - {}
  podSelector: {}
  policyTypes:
  - Egress`

const kubeconfigPath = "/kind/worker-cluster.kubeconfig"

// NewAction returns a new action for installing default CAPI
func NewAction(vaultPassword string, descriptorName string) actions.Action {
	return &action{
		vaultPassword:  vaultPassword,
		descriptorName: descriptorName,
	}
}

// Execute runs the action
func (a *action) Execute(ctx *actions.ActionContext) error {

	// Get the target node
	node, err := getNode(ctx)
	if err != nil {
		return err
	}

	// Parse the cluster descriptor
	descriptorFile, err := cluster.GetClusterDescriptor(a.descriptorName)
	if err != nil {
		return errors.Wrap(err, "failed to parse cluster descriptor")
	}

	// Get the secrets
	credentials, githubToken, err := getSecrets(*descriptorFile, a.vaultPassword)
	if err != nil {
		return err
	}

	envVars := []string{}
	if descriptorFile.InfraProvider == "aws" {
		envVars = getAWSEnv(credentials, githubToken)
	}

	ctx.Status.Start("Installing CAPx in local 🎖️")
	defer ctx.Status.End(false)

	err = installCAPALocal(node, ctx, envVars)
	if err != nil {
		return err
	}

	// mark success
	ctx.Status.End(true) // End Installing CAPx in local

	ctx.Status.Start("Generating worker cluster manifests 📝")
	defer ctx.Status.End(false)

	capiClustersNamespace := "capi-clusters"

	// Generate the cluster manifest
	descriptorData, err := cluster.GetClusterManifest(*descriptorFile)
	if err != nil {
		return errors.Wrap(err, "failed to generate cluster manifests")
	}

	// Create the cluster manifests file in the container
	descriptorPath := "/kind/manifests/cluster_" + descriptorFile.ClusterID + ".yaml"
	raw := bytes.Buffer{}
	cmd := node.Command("sh", "-c", "echo \""+descriptorData+"\" > "+descriptorPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to write the cluster manifests")
	}

	ctx.Status.End(true) // End Generating worker cluster manifests

	_, err = os.Stat("./secrets.yml")
	if err != nil {
		ctx.Status.Start("Generating secrets file 📝🗝️")
		defer ctx.Status.End(false)

		rewriteDescriptorFile(a.descriptorName)

		filelines := []string{
			"secrets:\n",
			"  github_token: " + githubToken + "\n",
			"  " + descriptorFile.InfraProvider + ":\n", "    credentials:\n",
			"      access_key: " + credentials["AccessKey"] + "\n",
			"      account: " + credentials["Account"] + "\n",
			"      region: " + descriptorFile.Region + "\n",
			"      secret_key: " + credentials["SecretKey"] + "\n",
		}

		basepath, err := currentdir()
		err = createDirectory(basepath)
		if err != nil {
			fmt.Println(err)
			return err
		}
		filename := basepath + "/secrets.yml"
		err = writeFile(filename, filelines)
		if err != nil {
			fmt.Println(err)
			return err
		}
		err = encryptFile(filename, a.vaultPassword)
		if err != nil {
			fmt.Println(err)
			return err
		}

		defer ctx.Status.End(true) // End Generating secrets file
	}

	ctx.Status.Start("Creating the worker cluster 💥")
	defer ctx.Status.End(false)

	// Create namespace for CAPI clusters (it must exists)
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "create", "ns", capiClustersNamespace)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to create cluster's Namespace")
	}

	// Apply cluster manifests
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "create", "-n", capiClustersNamespace, "-f", descriptorPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply manifests")
	}

	var machineHealthCheck = `
apiVersion: cluster.x-k8s.io/v1alpha3
kind: MachineHealthCheck
metadata:
  name: ` + descriptorFile.ClusterID + `-node-unhealthy
spec:
  clusterName: ` + descriptorFile.ClusterID + `
  nodeStartupTimeout: 300s
  selector:
    matchLabels:
      cluster.x-k8s.io/cluster-name: ` + descriptorFile.ClusterID + `
  unhealthyConditions:
    - type: Ready
      status: Unknown
      timeout: 60s
    - type: Ready
      status: 'False'
      timeout: 60s`

	// Create the MachineHealthCheck manifest file in the container
	machineHealthCheckPath := "/kind/machinehealthcheck.yaml"
	raw = bytes.Buffer{}
	cmd = node.Command("sh", "-c", "echo \""+machineHealthCheck+"\" > "+machineHealthCheckPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to write the MachineHealthCheck manifest")
	}

	// Enable the cluster's self-healing
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "-n", capiClustersNamespace, "apply", "-f", machineHealthCheckPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply the MachineHealthCheck manifest")
	}

	// Wait for the worker cluster creation
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "-n", capiClustersNamespace, "wait", "--for=condition=ready", "--timeout", "25m", "cluster", descriptorFile.ClusterID)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to create the worker Cluster")
	}

	// Wait for machines creation
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "-n", capiClustersNamespace, "wait", "--for=condition=ready", "--timeout", "20m", "--all", "md")
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to create the Machines")
	}

	ctx.Status.End(true) // End Creating the worker cluster

	ctx.Status.Start("Installing CAPx in EKS 🎖️")
	defer ctx.Status.End(false)

	// Create the allow-all-egress network policy file in the container
	allowAllEgressNetPolPath := "/kind/allow-all-egress_netpol.yaml"
	raw = bytes.Buffer{}
	cmd = node.Command("sh", "-c", "echo \""+allowAllEgressNetPol+"\" > "+allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to write the allow-all-egress network policy")
	}

	// Get worker cluster's kubeconfig file (in EKS the token last 10m, which should be enough)
	raw = bytes.Buffer{}
	cmd = node.Command("sh", "-c", "clusterctl -n "+capiClustersNamespace+" get kubeconfig "+descriptorFile.ClusterID+" > "+kubeconfigPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to get the kubeconfig file")
	}

	// AWS/EKS specific
	err = installCAPAWorker(node, envVars, kubeconfigPath, allowAllEgressNetPolPath)
	if err != nil {
		return err
	}

	// Scale CAPI to 2 replicas
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capi-system", "scale", "--replicas", "2", "deploy", "capi-controller-manager")
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to scale the CAPI Deployment")
	}

	// Allow egress in CAPI's Namespaces
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capi-system", "apply", "-f", allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply CAPI's NetworkPolicy")
	}
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capi-kubeadm-bootstrap-system", "apply", "-f", allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply CAPI's NetworkPolicy")
	}
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capi-kubeadm-control-plane-system", "apply", "-f", allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply CAPI's NetworkPolicy")
	}

	// Allow egress in cert-manager Namespace
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "cert-manager", "apply", "-f", allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply cert-manager's NetworkPolicy")
	}

	ctx.Status.End(true) // End Installing CAPx in worker cluster

	ctx.Status.Start("Transfering the management role 🗝️")
	defer ctx.Status.End(false)

	// Create namespace for CAPI clusters (it must exists) in worker cluster
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "create", "ns", capiClustersNamespace)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to create manifests Namespace")
	}

	// EKS specific: Pivot management role to worker cluster
	raw = bytes.Buffer{}
	cmd = node.Command("sh", "-c", "clusterctl move -n "+capiClustersNamespace+" --to-kubeconfig "+kubeconfigPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to pivot management role to worker cluster")
	}

	ctx.Status.End(true) // End Transfering the management role

	return nil
}
