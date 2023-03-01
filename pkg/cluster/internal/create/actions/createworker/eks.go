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
	"os"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/kind/pkg/cluster/internal/create/actions"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/cluster/nodeutils"
	"sigs.k8s.io/kind/pkg/commons"
	"sigs.k8s.io/kind/pkg/errors"
)

const CAPICoreProvider = "cluster-api:v1.3.2"
const CAPIBootstrapProvider = "kubeadm:v1.3.2"
const CAPIControlPlaneProvider = "kubeadm:v1.3.2"
const CAPIInfraProvider = "aws:v2.0.2"

// installCAPAWorker generates and apply the EKS manifests
func installCAPAWorker(aws commons.AWSCredentials, githubToken string, node nodes.Node, kubeconfigPath string, allowAllEgressNetPolPath string) error {

	// Install CAPA in worker cluster
	raw := bytes.Buffer{}
	cmd := node.Command("sh", "-c", "clusterctl --kubeconfig "+kubeconfigPath+" init --wait-providers --infrastructure "+
		CAPIInfraProvider+" --core "+CAPICoreProvider+" --bootstrap "+CAPIBootstrapProvider+" --control-plane "+CAPIControlPlaneProvider)
	cmd.SetEnv("AWS_REGION="+aws.Credentials.Region,
		"AWS_ACCESS_KEY_ID="+aws.Credentials.AccessKey,
		"AWS_SECRET_ACCESS_KEY="+aws.Credentials.SecretKey,
		"AWS_B64ENCODED_CREDENTIALS="+generateB64Credentials(aws.Credentials.AccessKey, aws.Credentials.SecretKey, aws.Credentials.Region),
		"GITHUB_TOKEN="+githubToken,
		"CAPA_EKS_IAM=true")
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to install CAPA")
	}

	//Scale CAPA to 2 replicas
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capa-system", "scale", "--replicas", "2", "deploy", "capa-controller-manager")
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to scale the CAPA Deployment")
	}

	// Allow egress in CAPA's Namespace
	raw = bytes.Buffer{}
	cmd = node.Command("kubectl", "--kubeconfig", kubeconfigPath, "-n", "capa-system", "apply", "-f", allowAllEgressNetPolPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to apply CAPA's NetworkPolicy")
	}

	// TODO STG: Disable OIDC provider

	return nil
}

// installCAPALocal installs CAPA in the local cluster
func installCAPALocal(ctx *actions.ActionContext, vaultPassword string, descriptorName string) error {

	ctx.Status.Start("[CAPA] Ensuring IAM security 👮")
	defer ctx.Status.End(false)

	allNodes, err := ctx.Nodes()
	if err != nil {
		return err
	}

	// get the target node for this task
	controlPlanes, err := nodeutils.ControlPlaneNodes(allNodes)
	if err != nil {
		return err
	}
	node := controlPlanes[0] // kind expects at least one always

	descriptorRAW, err := os.ReadFile("./" + descriptorName)
	if err != nil {
		return err
	}

	var descriptorFile commons.DescriptorFile
	err = yaml.Unmarshal(descriptorRAW, &descriptorFile)
	if err != nil {
		return err
	}

	aws, github_token, err := getCredentials(descriptorFile, vaultPassword)
	if err != nil {
		return err
	}

	eksConfigData := `
apiVersion: bootstrap.aws.infrastructure.cluster.x-k8s.io/v1beta1
kind: AWSIAMConfiguration
spec:
  bootstrapUser:
    enable: true
  eks:
    enable: true
    iamRoleCreation: false
    defaultControlPlaneRole:
        disable: false
  controlPlane:
    enableCSIPolicy: true
  nodes:
    extraPolicyAttachments:
    - arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy`

	// Create the eks.config file in the container
	var raw bytes.Buffer
	eksConfigPath := "/kind/eks.config"
	cmd := node.Command("sh", "-c", "echo \""+eksConfigData+"\" > "+eksConfigPath)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to create eks.config")
	}

	// Run clusterawsadm with the eks.config file previously created
	// (this will create or update the CloudFormation stack in AWS)
	raw = bytes.Buffer{}
	cmd = node.Command("clusterawsadm", "bootstrap", "iam", "create-cloudformation-stack", "--config", eksConfigPath)
	cmd.SetEnv("AWS_REGION="+aws.Credentials.Region,
		"AWS_ACCESS_KEY_ID="+aws.Credentials.AccessKey,
		"AWS_SECRET_ACCESS_KEY="+aws.Credentials.SecretKey,
		"GITHUB_TOKEN="+github_token)
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to run clusterawsadm")
	}
	ctx.Status.End(true) // End Ensuring CAPx requirements

	// Install CAPA
	raw = bytes.Buffer{}
	cmd = node.Command("sh", "-c", "clusterctl init --wait-providers --infrastructure "+
		CAPIInfraProvider+" --core "+CAPICoreProvider+" --bootstrap "+CAPIBootstrapProvider+" --control-plane "+CAPIControlPlaneProvider)
	cmd.SetEnv("AWS_REGION="+aws.Credentials.Region,
		"AWS_ACCESS_KEY_ID="+aws.Credentials.AccessKey,
		"AWS_SECRET_ACCESS_KEY="+aws.Credentials.SecretKey,
		"AWS_B64ENCODED_CREDENTIALS="+generateB64Credentials(aws.Credentials.AccessKey, aws.Credentials.SecretKey, aws.Credentials.Region),
		"GITHUB_TOKEN="+github_token,
		"CAPA_EKS_IAM=true")
	// "EXP_MACHINE_POOL=true")
	if err := cmd.SetStdout(&raw).Run(); err != nil {
		return errors.Wrap(err, "failed to install CAPA")
	}

	return nil
}
