#!/usr/bin/env bash
set -Eeuo pipefail

readonly PROGRAM_NAME=standalone-kubelet-deploy
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
PROJECT_DIR=$(cd -- "${SCRIPT_DIR}/../.." && pwd)
readonly PROJECT_DIR
# shellcheck source=deploy/scripts/standalone-common.sh
source "${SCRIPT_DIR}/standalone-common.sh"

usage() {
	cat <<'EOF'
Usage:
  deploy/scripts/deploy-standalone.sh --image IMAGE@sha256:DIGEST [options]

Required:
  --image IMAGE@sha256:DIGEST  Immutable image published by the container workflow.

Options:
  --node-name NAME             Current Kubernetes node name (default: hostname).
  --state-dir PATH             Persistent state and backup directory.
  --tls-server-name NAME       API Server TLS name in Route Controller kubeconfig.
  --timeout SECONDS            Timeout for each convergence step (default: 300).
  --cilium-chart PATH          Local chart path; enable external ClusterIP if needed.
  --skip-cilium-cleanup        Keep old Cilium host state; only for pre-cleaned nodes.
  --allow-workloads            Permit deleting a node with non-DaemonSet workloads.
  --check                      Validate this node without changing cluster resources.
  --yes                        Confirm mutations without an interactive prompt.
  -h, --help                   Show this help.

The script changes only the current node. Run it manually on each control-plane node.
It does not modify API Server EgressSelectorConfiguration or Konnectivity resources.
EOF
}

node_name=$(hostname)
route_image=
STATE_DIR=/var/lib/standalone-kubelet-manager
tls_server_name=apiserver.cluster.local
TIMEOUT=300
cilium_chart=
skip_cilium_cleanup=0
allow_workloads=0
check_only=0
ASSUME_YES=0
BUNDLE_DIR=

while (($#)); do
	case $1 in
	--node-name)
		node_name=$2
		shift 2
		;;
	--image)
		route_image=$2
		shift 2
		;;
	--state-dir)
		STATE_DIR=$2
		shift 2
		;;
	--tls-server-name)
		tls_server_name=$2
		shift 2
		;;
	--timeout)
		TIMEOUT=$2
		shift 2
		;;
	--cilium-chart)
		cilium_chart=$2
		shift 2
		;;
	--skip-cilium-cleanup)
		skip_cilium_cleanup=1
		shift
		;;
	--allow-workloads)
		allow_workloads=1
		shift
		;;
	--check)
		check_only=1
		shift
		;;
	--yes)
		ASSUME_YES=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done

[[ $EUID -eq 0 ]] || die "must run as root"
[[ $STATE_DIR == /* ]] || die "--state-dir must be absolute"
[[ $TIMEOUT =~ ^[1-9][0-9]*$ ]] || die "--timeout must be a positive integer"
validate_node_name "${node_name}"
if ((! check_only)); then
	[[ -n $route_image ]] || die "--image is required for deployment"
fi
if [[ -n $route_image ]]; then
	[[ $route_image =~ ^[A-Za-z0-9._/:@+-]+@sha256:[a-f0-9]{64}$ ]] ||
		die "--image must use an immutable sha256 digest"
fi
require_command bash
require_command install
require_command mktemp

trap remove_bundle EXIT
create_bundle

cluster_args=()
[[ -n $cilium_chart ]] && cluster_args+=(--cilium-chart "${cilium_chart}")
local_action cluster-check "${node_name}" "${cluster_args[@]}"

node_args=()
((skip_cilium_cleanup)) && node_args+=(--skip-cilium-cleanup)
((allow_workloads)) && node_args+=(--allow-workloads)

if ((check_only)); then
	local_action check-deploy "${node_name}" "${node_args[@]}"
	log "read-only checks passed on ${node_name}"
	exit 0
fi

local_action preflight-install "${node_name}" "${node_args[@]}"
confirm_or_exit "Convert ${node_name} to standalone Kubelet mode?"

if [[ -n $cilium_chart ]]; then
	local_action configure-cilium "${node_name}" --cilium-chart "${cilium_chart}"
	local_action cluster-check "${node_name}"
fi

node_args+=(--image "${route_image}" --tls-server-name "${tls_server_name}")
local_action install "${node_name}" "${node_args[@]}"
local_action cluster-check "${node_name}"
local_action check "${node_name}"
log "deployment completed on ${node_name}"
