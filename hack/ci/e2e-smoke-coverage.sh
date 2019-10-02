#!/usr/bin/env bash
# Copyright 2019 The Kubernetes Authors.
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
# Usage: SKIP="ginkgo skip regex" FOCUS="ginkgo focus regex" kind-e2e.sh

set -o errexit -o nounset -o pipefail -o xtrace

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd -P)"

# our exit handler (trap)
cleanup() {
  # remove our tempdir, this needs to be last, or it will prevent kind delete
  [[ -n "${TMP_DIR:-}" ]] && rm -rf "${TMP_DIR:?}"
}

# install kind to a tempdir GOPATH from this script's kind checkout
install_kind_with_coverage() {
  mkdir -p "${TMP_DIR}/bin"
  make -C "${REPO_ROOT}" install INSTALL_PATH="${TMP_DIR}/bin"

  hack/go_container.sh go test -c -o /out/kind.covered -covermode count \
    -coverpkg sigs.k8s.io/kind/... -tags=coverage ./pkg/internal/coverage
  cp bin/kind.covered "${TMP_DIR}/bin/kind"

  export PATH="${TMP_DIR}/bin:${PATH}"
}

main() {
  # create temp dir and setup cleanup
  TMP_DIR=$(mktemp -d)
  trap cleanup EXIT

  # install kind with coverage instrumentation
  install_kind_with_coverage

  # smoke test
   
}

main
