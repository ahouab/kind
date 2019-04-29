#!/usr/bin/env bash
# Copyright 2018 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# hack script for running a kind e2e
# must be run with a kubernetes checkout in $PWD (IE from the checkout)
# TODO(bentheelder): replace this with kubetest integration
# Usage: SKIP="ginkgo skip regex" FOCUS="ginkgo focus regex" kind-e2e.sh

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

# our exit handler (trap)
cleanup() {
    # always attempt to dump logs
    kind "export" logs "${ARTIFACTS}/logs" || true
    # KIND_IS_UP is true once we: kind create
    if [[ "${KIND_IS_UP:-}" = true ]]; then
        kind delete cluster || true
    fi
    # clean up e2e.test symlink
    rm -f _output/bin/e2e.test
    # remove our tempdir
    # NOTE: this needs to be last, or it will prevent kind delete
    if [[ -n "${TMP_DIR:-}" ]]; then
        rm -rf "${TMP_DIR}"
    fi
}

# install kind to a tempdir GOPATH from this script's kind checkout
install_kind() {
    # install `kind` to tempdir
    TMP_DIR=$(mktemp -d)
    # ensure bin dir
    mkdir -p "${TMP_DIR}/bin"
    # if we have a kind checkout, install that to the tmpdir, otherwise go get it
    if [[ $(go list sigs.k8s.io/kind) = "sigs.k8s.io/kind" ]]; then
        env "GOBIN=${TMP_DIR}/bin" go install sigs.k8s.io/kind
    else
        env "GOPATH=${TMP_DIR}" go get -u sigs.k8s.io/kind
    fi
    PATH="${TMP_DIR}/bin:${PATH}"
    export PATH
}

# build kubernetes / node image, e2e binaries
build() {
    # possibly enable bazel build caching before building kubernetes
    BAZEL_REMOTE_CACHE_ENABLED=${BAZEL_REMOTE_CACHE_ENABLED:-false}
    if [[ "${BAZEL_REMOTE_CACHE_ENABLED}" == "true" ]]; then
        # run the script in the kubekins image, do not fail if it fails
        /usr/local/bin/create_bazel_cache_rcs.sh || true
    fi

    # build the node image w/ kubernetes
    # TODO(bentheelder): remove the kube-root flag after we make kind try to
    # find this in a go module compatible way
    kind build node-image --type=bazel \
        --kube-root="$(go env GOPATH)/src/k8s.io/kubernetes"

    # make sure we have e2e requirements
    #make all WHAT="cmd/kubectl test/e2e/e2e.test vendor/github.com/onsi/ginkgo/ginkgo"
    bazel build //cmd/kubectl //test/e2e:e2e.test //vendor/github.com/onsi/ginkgo/ginkgo

    # e2e.test does not show up in a path with platform in it and will not be found
    # by kube::util::find-binary, so we will copy it to an acceptable location
    # until this is fixed upstream
    # https://github.com/kubernetes/kubernetes/issues/68306
    mkdir -p "_output/bin/"
    cp "bazel-bin/test/e2e/e2e.test" "_output/bin/"

    # try to make sure the kubectl we built is in PATH
    local maybe_kubectl
    maybe_kubectl="$(find "${PWD}/bazel-bin/" -name "kubectl" -type f)"
    if [[ -n "${maybe_kubectl}" ]]; then
        PATH="$(dirname "${maybe_kubectl}"):${PATH}"
        export PATH
    fi

    # release some memory after building
    sync || true
    echo 1 > /proc/sys/vm/drop_caches || true
}

# up a cluster with kind
create_cluster() {
    # create the audit-policy necessary for API Coverage
    # https://kubernetes.io/docs/tasks/debug-application-cluster/audit/#audit-policy
    cp $(dirname $0)/audit-policy.yaml /tmp/audit-policy.yaml
    touch "${ARTIFACTS}/apiserver-audit.log"
    # create the config file
    cat <<EOF > "${ARTIFACTS}/kind-config.yaml"
# config for 1 control plane node and 2 workers
# necessary for conformance
kind: Cluster
apiVersion: kind.sigs.k8s.io/v1alpha3
nodes:
# the control plane node / apiservers
- role: control-plane
  extraMounts:
  - hostPath: /tmp/audit-policy.yaml
    containerPath: /etc/kubernetes/audit-policy.yaml
  - hostPath: "${ARTIFACTS}/apiserver-audit.log"
    containerPath: /var/log/apiserver-audit.log
- role: worker
- role: worker
kubeadmConfigPatches:
- |
  kind: ClusterConfiguration
  metadata:
    name: config
EOF
    env
    find /
    #KUBEADM_MINOR=$(kubeadm version 2>&1 | perl -pe 's/(^.*Minor:")([0-9]+)(.*$)/\2/')
    #kubeadm version >  "${ARTIFACTS}/kubeadmi-version"
    #echo $KUBEADM_MINOR > "${ARTIFACTS}/kubeadm_minor"
    if echo $JOB_NAME | grep 1-11\\\|1-12\\\|1-13
    then
        echo Patching for kubeadm.k8s.io/v1alpha2
        cat <<ALPHA_CONFIG >> "${ARTIFACTS}/kind-config.yaml"
  # v1alpha2 works for kubeadm 1.11-1.12
  apiVersion: kubeadm.k8s.io/v1alpha2
  apiServer:
    extraArgs:
      audit-log-path: /var/log/apiserver-audit.log
      audit-policy-file: /etc/kubernetes/audit-policy.yaml
  apiServerExtraVolumes:
  - name: auditpolicy
    pathType: File
    readOnly: true
    hostPath: /etc/kubernetes/audit-policy.yaml
    mountPath: /etc/kubernetes/audit-policy.yaml
  - name: auditlog
    pathType: File
    readOnly: false
    hostPath: /var/log/apiserver-audit.log
    mountPath: /var/log/apiserver-audit.log
ALPHA_CONFIG
    else
        echo Patching for kubeadm.k8s.io/v1beta1
        cat <<BETA1_CONFIG >> "${ARTIFACTS}/kind-config.yaml"
  # v1beta1 works for 1.13-1.14
  apiVersion: kubeadm.k8s.io/v1beta1
  apiServer:
    extraArgs:
      audit-log-path: /var/log/apiserver-audit.log
      audit-policy-file: /etc/kubernetes/audit-policy.yaml
    extraVolumes:
    - name: auditpolicy
      pathType: File
      readOnly: true
      hostPath: /etc/kubernetes/audit-policy.yaml
      mountPath: /etc/kubernetes/audit-policy.yaml
    - name: auditlog
      pathType: File
      readOnly: false
      hostPath: /var/log/apiserver-audit.log
      mountPath: /var/log/apiserver-audit.log
BETA1_CONFIG
    fi
    # mark the cluster as up for cleanup
    # even if kind create fails, kind delete can clean up after it
    KIND_IS_UP=true
    # actually create, with:
    # - do not delete created nodes from a failed cluster create (for debugging)
    # - wait up to one minute for the nodes to be "READY"
    # - set log leve to debug
    # - use our multi node config
    kind create cluster \
        --image=kindest/node:latest \
        --retain \
        --wait=1m \
        --loglevel=debug \
        "--config=${ARTIFACTS}/kind-config.yaml"
}

# run e2es with kubetest
run_tests() {
    # export the KUBECONFIG
    KUBECONFIG="$(kind get kubeconfig-path)"
    export KUBECONFIG

    # base kubetest args
    KUBETEST_ARGS="--provider=skeleton --test --check-version-skew=false"

    # get the number of worker nodes
    # TODO(bentheelder): this is kinda gross
    NUM_NODES="$(kubectl get nodes \
        -o=jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.taints}{"\n"}{end}' \
        | grep -cv "node-role.kubernetes.io/master" \
    )"

    # ginkgo regexes
    SKIP="${SKIP:-"Alpha|Kubectl|\\[(Disruptive|Feature:[^\\]]+|Flaky)\\]"}"
    FOCUS="${FOCUS:-"\\[Conformance\\]"}"
    # if we set PARALLEL=true, skip serial tests set --ginkgo-parallel
    PARALLEL="${PARALLEL:-false}"
    if [[ "${PARALLEL}" == "true" ]]; then
        SKIP="\\[Serial\\]|${SKIP}"
        KUBETEST_ARGS="${KUBETEST_ARGS} --ginkgo-parallel"
    fi

    # add ginkgo args
    KUBETEST_ARGS="${KUBETEST_ARGS} --test_args=\"--ginkgo.focus=${FOCUS} --ginkgo.skip=${SKIP} --report-dir=${ARTIFACTS} --disable-log-dump=true --num-nodes=${NUM_NODES}\""

    # setting this env prevents ginkg e2e from trying to run provider setup
    export KUBERNETES_CONFORMANCE_TEST="y"

    # run kubetest, if it fails clean up and exit failure
    eval "kubetest ${KUBETEST_ARGS}"
}

# setup kind, build kubernetes, create a cluster, run the e2es
main() {
    # ensure artifacts exists when not in CI
    ARTIFACTS="${ARTIFACTS:-${PWD}/_artifacts}"
    mkdir -p "${ARTIFACTS}"
    export ARTIFACTS
    # now build an run the cluster and tests
    trap cleanup EXIT
    install_kind
    build
    create_cluster
    run_tests
}

main
