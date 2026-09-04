#!/usr/bin/env bash

log() {
	printf '[%s] %s\n' "${PROGRAM_NAME}" "$*"
}

die() {
	printf '[%s] ERROR: %s\n' "${PROGRAM_NAME}" "$*" >&2
	exit 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"
}

validate_node_name() {
	[[ $1 =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] || die "invalid Kubernetes node name: $1"
}

create_bundle() {
	BUNDLE_DIR=$(mktemp -d)
	export BUNDLE_DIR
	install -d -m 700 "${BUNDLE_DIR}/deploy"
	install -m 600 "${PROJECT_DIR}/deploy/standalone/kubelet-standalone-merge.json" \
		"${BUNDLE_DIR}/kubelet-standalone-merge.json"
	install -m 644 "${PROJECT_DIR}/deploy/standalone/20-standalone.conf" \
		"${BUNDLE_DIR}/20-standalone.conf"
	install -m 600 "${PROJECT_DIR}/deploy/rbac/rbac.yaml" \
		"${BUNDLE_DIR}/deploy/route-controller-rbac.yaml"
	install -m 600 "${PROJECT_DIR}/deploy/static-pod/image.yaml" \
		"${BUNDLE_DIR}/deploy/route-controller-static-pod.yaml"
}

remove_bundle() {
	if [[ -n ${BUNDLE_DIR:-} && -d ${BUNDLE_DIR} ]]; then
		rm -rf -- "${BUNDLE_DIR}"
	fi
}

local_action() {
	local action=$1
	local node_name=$2
	shift 2
	bash "${SCRIPT_DIR}/standalone-node.sh" "${action}" \
		--bundle-dir "${BUNDLE_DIR}" \
		--state-dir "${STATE_DIR}" \
		--node-name "${node_name}" \
		--timeout "${TIMEOUT}" \
		"$@"
}

confirm_or_exit() {
	local prompt=$1
	if ((ASSUME_YES)); then
		return
	fi
	[[ -t 0 ]] || die "non-interactive execution requires --yes"
	printf '%s [y/N] ' "${prompt}"
	local answer
	read -r answer
	[[ $answer == y || $answer == Y ]] || die "cancelled"
}
