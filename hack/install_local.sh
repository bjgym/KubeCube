#!/usr/bin/env bash

#Copyright 2021 KubeCube Authors
#
#Licensed under the Apache License, Version 2.0 (the "License");
#you may not use this file except in compliance with the License.
#You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
#Unless required by applicable law or agreed to in writing, software
#distributed under the License is distributed on an "AS IS" BASIS,
#WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#See the License for the specific language governing permissions and
#limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

if [[ "$(uname)" == "Darwin" ]]; then
    IPADDR=$(ifconfig | grep inet | grep -v inet6 | grep -v 127 | cut -d ' ' -f2 | sed -n '1p')
    KUBECONFIG=$(cat ~/.kube/config | base64)
elif [[ "$(expr substr $(uname -s) 1 5)" == "Linux" ]]; then
    IPADDR=$(hostname -I |awk '{print $1}')
    KUBECONFIG=$(cat ~/.kube/config | base64 -w 0)
elif [[ "$(expr substr $(uname -s) 1 10)" == "MINGW32_NT" ]]; then
    echo "not support for windows 32"
    exit 1
elif [[ "$(expr substr $(uname -s) 1 10)" == "MINGW64_NT" ]]; then
    echo "not support for windows 64"
fi

K8S_API="$1"

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"

# The chart is the only source of the platform's manifests.
#
# This path used to apply a second, hand-written copy of the RBAC, the webhook
# configurations and two of the configmaps. That copy had drifted: its
# ResourceQuota webhook was `failurePolicy: Fail` with no namespaceSelector, so
# a local install intercepted every ResourceQuota in the cluster, and its roles
# carried write verbs on namespaces and nodes. What is applied below is the same
# set the platform installs; what is left under deploy/manifests/ is only what
# makes a local run local.
CHART_DIR="${KUBECUBE_CHART_DIR:-${REPO_ROOT}/../kubecube-chart}"

function make_manifests() {
  sed s/#K8S_APIEndpoint/${K8S_API}/ deploy/template/pivotCluster.yaml | sed s/#KubeConfig/${KUBECONFIG}/ > deploy/manifests/pivotCluster.yaml
  sed s/#LOCAL_IP/${IPADDR}/ deploy/template/cubeLocalSvc.yaml > deploy/manifests/cubeLocalSvc.yaml
}

make_manifests

# The CRDs come from the generated sources, which is where `make manifests`
# writes them.
make install

kubectl create ns kubecube-system || true

# The platform's own objects, rendered from the chart. Only these templates: a
# full render would install the Deployments too, and this path runs the binary
# on the host instead.
helm template kubecube "${CHART_DIR}" \
  --namespace kubecube-system \
  --set-string global.componentsEnable.kubecube=true \
  --set-string global.componentsEnable.warden=true \
  --show-only templates/clusterrole.yaml \
  --show-only templates/clusterrolebind.yaml \
  --show-only templates/serviceaccount.yaml \
  --show-only templates/kubecube/auth-configmap.yaml \
  --show-only templates/kubecube/auth-mapping-configmap.yaml \
  --show-only templates/kubecube/feature-configmap.yaml \
  --show-only templates/kubecube/language-configmap.yaml \
  | kubectl apply -f -

# What makes a local run local: the pivot cluster's kubeconfig, the TLS material
# the chart's pre-install job would otherwise mint, the webhook configurations
# those secrets sign, and a NodePort to reach warden from outside the cluster.
kubectl apply -f deploy/manifests/pivotCluster.yaml
kubectl apply -f deploy/manifests/cubeLocalSvc.yaml
kubectl apply -f deploy/manifests/caSecret.yaml
kubectl apply -f deploy/manifests/tlsSecret.yaml
kubectl apply -f deploy/manifests/cubeWebhook.yaml
kubectl apply -f deploy/manifests/wardenWebhook.yaml
kubectl apply -f deploy/manifests/wardenNodePort.yaml
kubectl apply -f deploy/metrics-server.yaml
