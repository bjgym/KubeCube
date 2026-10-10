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

mkdir -p logs

# The three images cube deploys into a member cluster are declared once, in the
# chart's global.images. Read them from there instead of pinning them a second
# time here: this script used to pin hub.c.163.com/kubecube/*:v1.1.0 while the
# chart deployed docker.io/kubecube/*:v1.8.2, so a local run and a real install
# exercised different builds of warden. Export any of them before running this
# to point at an image of your own.
CHART_DIR="${KUBECUBE_CHART_DIR:-${REPO_ROOT}/../kubecube-chart}"
VALUES="${CHART_DIR}/values.yaml"
if [ ! -f "${VALUES}" ]; then
  echo "run_cube: ${VALUES} not found; set KUBECUBE_CHART_DIR to the kubecube-chart checkout" >&2
  exit 1
fi

# values_scalar BLOCK KEY - the value of "    KEY: value" inside "  BLOCK:"
# Deliberately inlined rather than sourced from the chart's own hack/ directory:
# this script lives in another repository, and a runtime dependency on a sibling
# checkout's internals would break more often than these eight lines drift.
values_scalar() {
  local block="$1" key="$2" in_block=0 line
  while IFS= read -r line; do
    line="${line%$'\r'}"
    case "${line}" in
      "  ${block}:") in_block=1; continue ;;
    esac
    if [ "${in_block}" = 1 ]; then
      case "${line}" in
        "    ${key}:"*) printf '%s' "${line#*: }"; return 0 ;;
        "  "*) ;;
        *) return 1 ;;
      esac
    fi
  done < "${VALUES}"
  return 1
}

hub_registry=$(values_scalar hub registry)
hub_project=$(values_scalar hub project)

# image KEY - the fully qualified reference for global.images.KEY, or non-zero
# when the key is absent. It returns rather than exits: it is always called
# inside a command substitution, whose subshell an exit could not escape.
image() {
  local value
  value=$(values_scalar images "$1")
  if [ -z "${value}" ]; then
    echo "run_cube: global.images.$1 is not set in ${VALUES}" >&2
    return 1
  fi
  printf '%s/%s/%s' "${hub_registry}" "${hub_project}" "${value}"
}

# Resolved one at a time so that a missing key aborts here, in the parent shell.
# Left inside the command substitution, the failure would be swallowed and an
# image reference ending in a slash would be exported and deployed.
if [ -z "${WARDEN_IMAGE:-}" ]; then
  WARDEN_IMAGE=$(image warden) || exit 1
  export WARDEN_IMAGE
fi
if [ -z "${DEPENDENCE_JOB_IMAGE:-}" ]; then
  DEPENDENCE_JOB_IMAGE=$(image dependenceJob) || exit 1
  export DEPENDENCE_JOB_IMAGE
fi
if [ -z "${WARDEN_INIT_IMAGE:-}" ]; then
  WARDEN_INIT_IMAGE=$(image wardenInit) || exit 1
  export WARDEN_INIT_IMAGE
fi
export JWT_SECRET=56F0D8DB90241C6E
export PIVOT_CUBE_HOST=kubecube:7443

go run -mod=vendor cmd/cube/main.go -log-level=debug -secure-port=7443 -tls-cert=deploy/tls/tls.crt -tls-key=deploy/tls/tls.key -webhook-cert=deploy/tls -webhook-server-port=9443 -leader-elect=false -log-file=logs/cube.log
