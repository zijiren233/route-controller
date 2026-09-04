#!/usr/bin/env bash
set -Eeuo pipefail

readonly PROGRAM_NAME=standalone-kubelet-rollback
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly SCRIPT_DIR
PROJECT_DIR=$(cd -- "${SCRIPT_DIR}/../.." && pwd)
readonly PROJECT_DIR
# shellcheck source=deploy/scripts/standalone-common.sh
source "${SCRIPT_DIR}/standalone-common.sh"

usage() {
	cat <<'EOF'
Usage:
  deploy/scripts/rollback-standalone.sh [options]

Options:
  --node-name NAME             Current Kubernetes node name (default: hostname).
  --state-dir PATH             State directory created by deploy-standalone.sh.
  --legacy-backup-dir PATH     Legacy backup directory on the current node.
  --timeout SECONDS            Timeout for each convergence step (default: 300).
  --cilium-chart PATH          Local chart path if deployment changed Cilium values.
  --delete-rbac                Delete shared Route Controller RBAC and token.
  --check                      Validate rollback inputs without changing the node.
  --yes                        Confirm mutations without an interactive prompt.
  -h, --help                   Show this help.

The script changes only the current node. Run it manually on each control-plane node.
Shared RBAC is preserved by default; delete it explicitly after the final rollback.
EOF
}

node_name=$(hostname)
STATE_DIR=/var/lib/standalone-kubelet-manager
legacy_backup_dir=
TIMEOUT=300
cilium_chart=
delete_rbac=0
check_only=0
ASSUME_YES=0
BUNDLE_DIR=

while (($#)); do
	case $1 in
	--node-name)
		node_name=$2
		shift 2
		;;
	--state-dir)
		STATE_DIR=$2
		shift 2
		;;
	--legacy-backup-dir)
		legacy_backup_dir=$2
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
	--delete-rbac)
		delete_rbac=1
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
[[ -z $legacy_backup_dir || $legacy_backup_dir == /* ]] ||
	die "--legacy-backup-dir must be absolute"
[[ $TIMEOUT =~ ^[1-9][0-9]*$ ]] || die "--timeout must be a positive integer"
validate_node_name "${node_name}"

require_command bash
require_command install
require_command mktemp

trap remove_bundle EXIT
create_bundle

rollback_args=()
[[ -n $legacy_backup_dir ]] && rollback_args+=(--legacy-backup-dir "${legacy_backup_dir}")

if ((check_only)); then
	local_action check-rollback "${node_name}" "${rollback_args[@]}"
	log "rollback inputs passed read-only checks on ${node_name}"
	exit 0
fi

confirm_or_exit "Restore Kubernetes Node registration on ${node_name}?"
local_action rollback "${node_name}" "${rollback_args[@]}"

cilium_args=()
[[ -n $cilium_chart ]] && cilium_args+=(--cilium-chart "${cilium_chart}")
local_action rollback-cilium "${node_name}" "${cilium_args[@]}"

if ((delete_rbac)); then
	local_action delete-rbac "${node_name}"
fi
local_action check "${node_name}"
log "rollback completed on ${node_name}"
