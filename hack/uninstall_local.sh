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

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"

# The same two halves the install applies, in reverse: what the chart renders
# and what makes a local run local. deploy/manifests/rbac, cubeWebhook's
# siblings and the two configmaps that used to live here are gone — the chart
# is their only source.
CHART_DIR="${KUBECUBE_CHART_DIR:-${REPO_ROOT}/../kubecube-chart}"

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
  | kubectl delete -f - --ignore-not-found

kubectl delete -f deploy/manifests --ignore-not-found
kubectl delete -f deploy/metrics-server.yaml --ignore-not-found
kubectl delete ns kubecube-system --ignore-not-found

make uninstall
