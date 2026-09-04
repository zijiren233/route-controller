#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly PROGRAM_NAME=standalone-kubelet-node
readonly KUBELET_CONFIG=/var/lib/kubelet/config.yaml
readonly STANDALONE_CONFIG=/var/lib/kubelet/standalone-config.yaml
readonly KUBELET_DROPIN=/etc/systemd/system/kubelet.service.d/20-standalone.conf
readonly ROUTE_CONFIG_DIR=/etc/kubernetes/route-controller
readonly ROUTE_MANIFEST=/etc/kubernetes/manifests/route-controller.yaml
readonly ROUTE_KUBECONFIG=${ROUTE_CONFIG_DIR}/kubeconfig
readonly ROUTE_PROTOCOL=99
readonly ADMIN_KUBECONFIG=/etc/kubernetes/admin.conf
export KUBECONFIG=${ADMIN_KUBECONFIG}

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

usage() {
	cat <<'EOF'
Internal node executor. Use deploy-standalone.sh or rollback-standalone.sh.
EOF
}

action=${1:-}
[[ -n $action ]] || {
	usage
	exit 2
}
shift

bundle_dir=
state_dir=/var/lib/standalone-kubelet-manager
node_name=$(hostname)
timeout=300
route_image=
tls_server_name=apiserver.cluster.local
legacy_backup_dir=
cilium_chart=
skip_cilium_cleanup=0
allow_workloads=0

while (($#)); do
	case $1 in
	--bundle-dir)
		bundle_dir=$2
		shift 2
		;;
	--state-dir)
		state_dir=$2
		shift 2
		;;
	--node-name)
		node_name=$2
		shift 2
		;;
	--timeout)
		timeout=$2
		shift 2
		;;
	--image)
		route_image=$2
		shift 2
		;;
	--tls-server-name)
		tls_server_name=$2
		shift 2
		;;
	--legacy-backup-dir)
		legacy_backup_dir=$2
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
	*)
		die "unknown argument: $1"
		;;
	esac
done

[[ $EUID -eq 0 ]] || die "must run as root"
[[ $state_dir == /* ]] || die "state directory must be absolute"
[[ $timeout =~ ^[1-9][0-9]*$ ]] || die "timeout must be a positive integer"
[[ $node_name =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]] || die "invalid node name: ${node_name}"

readonly backup_dir=${state_dir}/backup
readonly phase_file=${state_dir}/phase
readonly owner_file=${state_dir}/owner

write_phase() {
	local phase=$1
	local temporary
	temporary=$(mktemp "${state_dir}/phase.XXXXXX")
	printf '%s\n' "${phase}" >"${temporary}"
	mv -f -- "${temporary}" "${phase_file}"
}

phase_is() {
	[[ -f $phase_file ]] && [[ $(<"${phase_file}") == "$1" ]]
}

current_phase() {
	if [[ -f $phase_file ]]; then
		printf '%s' "$(<"${phase_file}")"
	else
		printf '%s' unknown
	fi
}

wait_until() {
	local description=$1
	shift
	local deadline=$((SECONDS + timeout))
	until "$@"; do
		((SECONDS < deadline)) || die "timed out waiting for ${description}"
		sleep 1
	done
}

kubelet_healthy() {
	systemctl is-active --quiet kubelet &&
		curl -fsS --max-time 2 http://127.0.0.1:10248/healthz >/dev/null 2>&1
}

route_controller_ready() {
	curl -fsS --max-time 2 http://127.0.0.1:9919/readyz >/dev/null 2>&1
}

node_absent() {
	! kubectl get node "${node_name}" >/dev/null 2>&1
}

node_ready() {
	[[ $(kubectl get node "${node_name}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null) == True ]]
}

cilium_pod_ready() {
	kubectl -n kube-system get pods -l k8s-app=cilium \
		--field-selector "spec.nodeName=${node_name}" -o json 2>/dev/null |
		jq -e 'any(.items[]?; any(.status.conditions[]?; .type == "Ready" and .status == "True"))' >/dev/null
}

cilium_daemonset_converged() {
	local daemonset nodes
	daemonset=$(kubectl -n kube-system get daemonset cilium -o json 2>/dev/null) || return 1
	jq -e '
    (.status.desiredNumberScheduled // 0) > 0 and
    .status.observedGeneration >= .metadata.generation and
    .status.currentNumberScheduled == .status.desiredNumberScheduled and
    .status.updatedNumberScheduled == .status.desiredNumberScheduled and
    .status.numberReady == .status.desiredNumberScheduled and
    .status.numberAvailable == .status.desiredNumberScheduled and
    (.status.numberUnavailable // 0) == 0
  ' <<<"${daemonset}" >/dev/null || return 1
	nodes=$(kubectl get nodes -o json 2>/dev/null) || return 1
	kubectl -n kube-system get pods -l k8s-app=cilium -o json 2>/dev/null |
		jq -e --argjson nodes "${nodes}" '
      (.items | length) > 0 and
      all(.items[];
        . as $pod |
        $pod.metadata.deletionTimestamp == null and
        any($nodes.items[]; .metadata.name == $pod.spec.nodeName) and
        any($pod.status.conditions[]?; .type == "Ready" and .status == "True")
      )
    ' >/dev/null
}

cilium_runtime_stopped() {
	local running=
	running=$(crictl ps --quiet --name cilium 2>/dev/null || true)
	[[ -z $running ]] && ! pgrep -f '(^|/)(cilium-agent|cilium-envoy)( |$)' >/dev/null 2>&1
}

route_runtime_stopped() {
	[[ -z $(crictl ps --quiet \
		--label "io.kubernetes.pod.name=route-controller-${node_name}" 2>/dev/null || true) ]]
}

is_standalone() {
	[[ -f $KUBELET_DROPIN && -f $STANDALONE_CONFIG ]] &&
		grep -Eq '^Environment="KUBELET_KUBECONFIG_ARGS="$' "${KUBELET_DROPIN}" &&
		grep -Eq '^registerNode:[[:space:]]*false$' "${STANDALONE_CONFIG}"
}

check_node_status() {
	log "checking ${node_name}"
	kubelet_healthy || die "kubelet is not healthy"
	if is_standalone; then
		node_absent || die "standalone kubelet still has a Node object: ${node_name}"
		[[ -f $ROUTE_MANIFEST && -f $ROUTE_KUBECONFIG ]] ||
			die "Route Controller files are incomplete"
		route_controller_ready || die "Route Controller is not ready"
		[[ -z $(ip -4 route show table 254 proto "${ROUTE_PROTOCOL}") ]] &&
			die "Route Controller has no protocol ${ROUTE_PROTOCOL} routes"
		log "standalone kubelet and Route Controller are healthy"
		return
	fi
	node_ready || die "registered node is not Ready: ${node_name}"
	log "registered kubelet is healthy"
}

check_cluster() {
	require_command kubectl
	require_command jq
	kubectl get --raw=/readyz >/dev/null
	wait_until "Cilium DaemonSet and Pod convergence" cilium_daemonset_converged
	local desired ready external pod value
	desired=$(kubectl -n kube-system get daemonset cilium -o jsonpath='{.status.desiredNumberScheduled}')
	ready=$(kubectl -n kube-system get daemonset cilium -o jsonpath='{.status.numberReady}')
	[[ -n $desired && $ready == "$desired" ]] || die "Cilium DaemonSet is not fully ready: ${ready}/${desired}"
	external=$(kubectl -n kube-system get configmap cilium-config \
		-o jsonpath='{.data.bpf-lb-external-clusterip}')
	if [[ $external != true ]]; then
		[[ -n $cilium_chart && -d $cilium_chart ]] ||
			die "Cilium bpf.lbExternalClusterIP must be true or --cilium-chart must be provided"
		log "Cilium external ClusterIP handling will be enabled from ${cilium_chart}"
	fi
	while IFS= read -r pod; do
		[[ -n $pod ]] || continue
		value=$(kubectl -n kube-system exec "${pod}" -c cilium-agent -- \
			cat /proc/sys/net/ipv4/ip_forward 2>/dev/null)
		[[ $value == 1 ]] || die "${pod}: net.ipv4.ip_forward must be 1"
		value=$(kubectl -n kube-system exec "${pod}" -c cilium-agent -- \
			cat /proc/sys/net/ipv4/conf/all/rp_filter 2>/dev/null)
		[[ $value == 0 ]] || die "${pod}: net.ipv4.conf.all.rp_filter must be 0"
	done < <(kubectl -n kube-system get pods -l k8s-app=cilium -o name)
	log "cluster API, Cilium readiness, external ClusterIP, and worker sysctls are valid"
}

configure_cilium() {
	require_command helm
	require_command kubectl
	[[ -n $cilium_chart && -d $cilium_chart ]] || die "Cilium chart directory does not exist: ${cilium_chart}"
	install -d -m 700 "${state_dir}/cluster"
	if [[ ! -f ${state_dir}/cluster/cilium-user-values.yaml ]]; then
		helm -n kube-system get values cilium -o yaml >"${state_dir}/cluster/cilium-user-values.yaml"
	fi
	if [[ $(kubectl -n kube-system get configmap cilium-config \
		-o jsonpath='{.data.bpf-lb-external-clusterip}') == true ]]; then
		log "Cilium external ClusterIP handling is already enabled"
		return
	fi
	touch "${state_dir}/cluster/cilium-changed"
	helm upgrade cilium "${cilium_chart}" -n kube-system --reuse-values \
		--set bpf.lbExternalClusterIP=true --wait --timeout "${timeout}s"
	kubectl -n kube-system rollout restart daemonset/cilium
	kubectl -n kube-system rollout status daemonset/cilium --timeout "${timeout}s"
	log "enabled Cilium external ClusterIP handling"
}

rollback_cilium() {
	if [[ ! -f ${state_dir}/cluster/cilium-changed ]]; then
		log "Cilium values were not changed by this deployment"
		return
	fi
	require_command helm
	require_command kubectl
	[[ -n $cilium_chart && -d $cilium_chart ]] ||
		die "--cilium-chart is required to restore the saved Cilium values"
	[[ -f ${state_dir}/cluster/cilium-user-values.yaml ]] || die "saved Cilium values are missing"
	helm upgrade cilium "${cilium_chart}" -n kube-system --reset-values \
		-f "${state_dir}/cluster/cilium-user-values.yaml" --wait --timeout "${timeout}s"
	kubectl -n kube-system rollout restart daemonset/cilium
	kubectl -n kube-system rollout status daemonset/cilium --timeout "${timeout}s"
	rm -f -- "${state_dir}/cluster/cilium-changed"
	log "restored Cilium values saved before deployment"
}

check_unmanaged_workloads() {
	local workloads
	workloads=$(kubectl get pods -A --field-selector "spec.nodeName=${node_name}" -o json |
		jq -r '
      .items[] |
      select(.metadata.annotations["kubernetes.io/config.mirror"] == null) |
      select(all(.metadata.ownerReferences[]?; .kind != "DaemonSet")) |
      "\(.metadata.namespace)/\(.metadata.name)"')
	if [[ -n $workloads && $allow_workloads -eq 0 ]]; then
		printf '%s\n' "${workloads}" >&2
		die "non-DaemonSet workloads are running on ${node_name}; move them or use --allow-workloads"
	fi
}

backup_original_state() {
	if [[ -d $backup_dir ]]; then
		[[ -f $owner_file && $(<"${owner_file}") == standalone-kubelet-deployer-v1 ]] ||
			die "existing state directory is not owned by this deployer: ${state_dir}"
		write_phase backed-up
		return
	fi
	if [[ -f $owner_file ]]; then
		[[ $(<"${owner_file}") == standalone-kubelet-deployer-v1 ]] ||
			die "existing state directory is not owned by this deployer: ${state_dir}"
	else
		local owner_temporary
		owner_temporary=$(mktemp "${state_dir}/owner.XXXXXX")
		printf '%s\n' standalone-kubelet-deployer-v1 >"${owner_temporary}"
		mv -f -- "${owner_temporary}" "${owner_file}"
	fi
	local backup_temporary
	backup_temporary=$(mktemp -d "${state_dir}/backup.XXXXXX")
	install -m 600 "${KUBELET_CONFIG}" "${backup_temporary}/kubelet-config.yaml"
	kubectl get node "${node_name}" -o json >"${backup_temporary}/node.json"
	chmod 600 "${backup_temporary}/node.json"
	if [[ -f $KUBELET_DROPIN ]]; then
		install -m 644 "${KUBELET_DROPIN}" "${backup_temporary}/20-standalone.conf"
		touch "${backup_temporary}/had-standalone-dropin"
	fi
	if [[ -f $STANDALONE_CONFIG ]]; then
		install -m 600 "${STANDALONE_CONFIG}" "${backup_temporary}/standalone-config.yaml"
		touch "${backup_temporary}/had-standalone-config"
	fi
	mv -- "${backup_temporary}" "${backup_dir}"
	write_phase backed-up
}

stage_cilium_cleanup_tool() {
	((skip_cilium_cleanup)) && return
	local pod
	pod=$(kubectl -n kube-system get pods -l k8s-app=cilium \
		--field-selector "spec.nodeName=${node_name}" \
		-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
	[[ -n $pod ]] || die "no Cilium Agent Pod found on ${node_name}"
	kubectl -n kube-system cp "${pod}:/usr/bin/cilium-dbg" "${state_dir}/cilium-dbg" \
		-c cilium-agent
	chmod 700 "${state_dir}/cilium-dbg"
}

create_route_kubeconfig() {
	local secret_data token_base64 ca_base64 route_token temporary_dir deadline
	kubectl apply -f "${bundle_dir}/deploy/route-controller-rbac.yaml"
	deadline=$((SECONDS + timeout))
	secret_data=
	until [[ -n $secret_data ]]; do
		((SECONDS < deadline)) || die "timed out waiting for Route Controller token"
		secret_data=$(kubectl -n kube-system get secret route-controller-token \
			-o jsonpath='{.data.token}{" "}{.data.ca\.crt}' 2>/dev/null || true)
		sleep 1
	done
	read -r token_base64 ca_base64 <<<"${secret_data}"
	[[ -n $token_base64 && -n $ca_base64 ]] || die "Route Controller token Secret is incomplete"
	temporary_dir=$(mktemp -d "${state_dir}/kubeconfig.XXXXXX")
	printf '%s' "${ca_base64}" | base64 --decode >"${temporary_dir}/ca.crt"
	route_token=$(printf '%s' "${token_base64}" | base64 --decode)
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-cluster local-apiserver \
		--server=https://127.0.0.1:6443 \
		--tls-server-name="${tls_server_name}" \
		--certificate-authority="${temporary_dir}/ca.crt" \
		--embed-certs=true >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-credentials route-controller \
		--token="${route_token}" >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-context route-controller \
		--cluster=local-apiserver --user=route-controller >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" use-context route-controller >/dev/null
	install -d -m 700 "${ROUTE_CONFIG_DIR}"
	install -m 600 "${temporary_dir}/kubeconfig" "${ROUTE_KUBECONFIG}"
	rm -rf -- "${temporary_dir}"
	kubectl --kubeconfig="${ROUTE_KUBECONFIG}" auth can-i list nodes 2>/dev/null | grep -qx yes ||
		die "Route Controller kubeconfig failed RBAC validation"
	kubectl --kubeconfig="${ROUTE_KUBECONFIG}" auth can-i list ciliumnodes.cilium.io 2>/dev/null |
		grep -qx yes ||
		die "Route Controller kubeconfig cannot list CiliumNode resources"
	kubectl --kubeconfig="${ROUTE_KUBECONFIG}" auth can-i list pods -n kube-system 2>/dev/null |
		grep -qx yes ||
		die "Route Controller kubeconfig cannot list Cilium Pods"
	for config_map in cilium-config kubeadm-config; do
		kubectl --kubeconfig="${ROUTE_KUBECONFIG}" auth can-i \
			get "configmap/${config_map}" -n kube-system 2>/dev/null | grep -qx yes ||
			die "Route Controller kubeconfig cannot get ConfigMap ${config_map}"
	done
}

generate_files() {
	local temporary
	temporary=$(mktemp -d "${state_dir}/render.XXXXXX")
	kubectl patch --local -f "${backup_dir}/kubelet-config.yaml" --type=merge \
		--patch-file "${bundle_dir}/kubelet-standalone-merge.json" -o yaml \
		>"${temporary}/standalone-config.yaml"
	grep -Eq '^registerNode:[[:space:]]*false$' "${temporary}/standalone-config.yaml" ||
		die "rendered Kubelet config is not standalone"
	install -m 600 "${temporary}/standalone-config.yaml" "${state_dir}/standalone-config.yaml"
	[[ $route_image =~ ^[A-Za-z0-9._/:@+-]+@sha256:[a-f0-9]{64}$ ]] ||
		die "Route Controller image must use an immutable sha256 digest"
	sed -E "s|^([[:space:]]+image:)[[:space:]]+.*route-controller.*$|\\1 ${route_image}|" \
		"${bundle_dir}/deploy/route-controller-static-pod.yaml" \
		>"${state_dir}/route-controller.yaml"
	grep -qF "image: ${route_image}" "${state_dir}/route-controller.yaml" ||
		die "failed to render Route Controller image"
	rm -f -- "${state_dir}/route-controller-config.yaml"
	rm -rf -- "${temporary}"
}

stage_route_image() {
	log "pulling immutable Route Controller image"
	crictl pull "${route_image}" >/dev/null
}

preflight_install() {
	for command_name in base64 crictl curl flock ip jq kubectl pgrep sed systemctl tar; do
		require_command "${command_name}"
	done
	[[ -n $bundle_dir && -d $bundle_dir ]] || die "bundle directory is missing"
	[[ -f /etc/kubernetes/admin.conf && -f $KUBELET_CONFIG ]] ||
		die "Kubernetes admin or Kubelet config is missing"
	kubelet_healthy || die "kubelet is not healthy"
	kubectl get --raw=/readyz >/dev/null
	if is_standalone; then
		if phase_is complete; then
			check_node_status
			return
		fi
		if [[ -f $owner_file ]]; then
			die "managed standalone conversion is incomplete at phase $(current_phase); run rollback"
		fi
		die "an unmanaged standalone Kubelet installation already exists"
	fi
	if [[ -f $owner_file && ! -d $backup_dir ]]; then
		[[ $(<"${owner_file}") == standalone-kubelet-deployer-v1 ]] ||
			die "existing state directory is not owned by this deployer: ${state_dir}"
	fi
	if [[ -d $backup_dir ]]; then
		[[ -f $owner_file && $(<"${owner_file}") == standalone-kubelet-deployer-v1 ]] ||
			die "existing state directory is not owned by this deployer: ${state_dir}"
		phase_is backed-up || phase_is rolled-back ||
			die "deployment is incomplete at phase $(current_phase); run rollback before retrying"
	fi
	kubectl get node "${node_name}" >/dev/null 2>&1 ||
		die "Node does not exist before deployment: ${node_name}"
	node_ready || die "Node is not Ready before deployment: ${node_name}"
	if [[ -e $ROUTE_MANIFEST || -e $ROUTE_CONFIG_DIR ]]; then
		phase_is backed-up || die "unmanaged Route Controller files already exist"
	fi
	grep -q -- '--egress-selector-config-file' /etc/kubernetes/manifests/kube-apiserver.yaml &&
		die "API Server still uses EgressSelectorConfiguration; remove it after validating direct routing"
	check_unmanaged_workloads
	if ((! skip_cilium_cleanup)); then
		local pod
		pod=$(kubectl -n kube-system get pods -l k8s-app=cilium \
			--field-selector "spec.nodeName=${node_name}" \
			-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
		[[ -n $pod ]] || die "no Cilium Agent Pod found on ${node_name}"
	fi
	log "deployment prerequisites are valid"
}

check_deploy() {
	if is_standalone; then
		check_node_status
		return
	fi
	preflight_install
}

activate_standalone_kubelet() {
	install -m 600 "${state_dir}/standalone-config.yaml" "${STANDALONE_CONFIG}"
	install -d -m 755 "$(dirname -- "${KUBELET_DROPIN}")"
	install -m 644 "${bundle_dir}/20-standalone.conf" "${KUBELET_DROPIN}"
	systemctl daemon-reload
	systemctl restart kubelet
	wait_until "standalone Kubelet health" kubelet_healthy
	is_standalone || die "Kubelet did not enter standalone mode"
	write_phase kubelet-standalone
	kubectl delete node "${node_name}" --wait=false
	kubectl -n kube-node-lease delete lease "${node_name}" --ignore-not-found
	wait_until "Node object deletion" node_absent
	write_phase node-deleted
}

cleanup_cilium_state() {
	((skip_cilium_cleanup)) && {
		log "skipping Cilium state cleanup by request"
		return
	}
	wait_until "Cilium runtime shutdown" cilium_runtime_stopped
	if ! "${state_dir}/cilium-dbg" post-uninstall-cleanup --all-state --force; then
		cilium_runtime_stopped || die "Cilium cleanup failed while Cilium is still running"
		if [[ -f /var/run/cilium/cilium.pid ]]; then
			rm -f -- /var/run/cilium/cilium.pid
			"${state_dir}/cilium-dbg" post-uninstall-cleanup --all-state --force
		else
			die "Cilium cleanup failed"
		fi
	fi
	for link in cilium_host cilium_net cilium_vxlan; do
		! ip link show "${link}" >/dev/null 2>&1 || die "Cilium link remains after cleanup: ${link}"
	done
	write_phase cilium-cleaned
}

install_route_controller() {
	install -d -m 700 "${ROUTE_CONFIG_DIR}"
	rm -f -- "${ROUTE_CONFIG_DIR}/config.yaml"
	install -m 600 "${state_dir}/route-controller.yaml" "${ROUTE_MANIFEST}"
	wait_until "Route Controller readiness" route_controller_ready
	[[ -n $(ip -4 route show table 254 proto "${ROUTE_PROTOCOL}") ]] ||
		die "Route Controller is ready but no managed routes exist"
	local service_ip status
	service_ip=$(kubectl get service kubernetes -n default -o jsonpath='{.spec.clusterIP}')
	status=$(curl --insecure --silent --show-error --output /dev/null --write-out '%{http_code}' \
		--max-time 5 "https://${service_ip}:443/readyz")
	[[ $status == 200 ]] || die "ClusterIP validation failed with HTTP ${status}"
	write_phase complete
	log "standalone Kubelet and Route Controller deployment completed"
}

install_node() {
	if phase_is complete; then
		check_node_status
		log "deployment is already complete"
		return
	fi
	install -d -m 700 "${state_dir}"
	exec 9>"${state_dir}/lock"
	flock -n 9 || die "another operation is using ${state_dir}"
	preflight_install
	backup_original_state
	stage_cilium_cleanup_tool
	create_route_kubeconfig
	generate_files
	stage_route_image
	activate_standalone_kubelet
	cleanup_cilium_state
	install_route_controller
}

stop_route_controller() {
	if [[ -f $ROUTE_MANIFEST ]]; then
		rm -f -- "${ROUTE_MANIFEST}"
		wait_until "Route Controller shutdown" route_runtime_stopped
	fi
}

restore_node_metadata() {
	local patch
	if [[ -n $rollback_node_json ]]; then
		patch=$(jq -c '{metadata:{labels:(.metadata.labels // {})},spec:{taints:(.spec.taints // [])}}' \
			"${rollback_node_json}")
	else
		patch=$(kubectl patch --local -f "${rollback_node_yaml}" --type=merge -p '{}' -o json |
			jq -c '{metadata:{labels:(.metadata.labels // {})},spec:{taints:(.spec.taints // [])}}')
	fi
	kubectl patch node "${node_name}" --type=merge -p "${patch}" >/dev/null
}

resolve_rollback_backup() {
	rollback_kubelet_config=
	rollback_node_json=
	rollback_node_yaml=
	rollback_dropin_dir=
	rollback_dropin_file=
	rollback_standalone_file=
	if [[ -n $legacy_backup_dir ]]; then
		[[ -d $legacy_backup_dir ]] || die "legacy backup directory does not exist: ${legacy_backup_dir}"
		rollback_kubelet_config=${legacy_backup_dir}/kubelet-config.yaml
		rollback_dropin_dir=${legacy_backup_dir}/kubelet-service.d
		if [[ -f ${legacy_backup_dir}/node.json ]]; then
			rollback_node_json=${legacy_backup_dir}/node.json
		elif [[ -f ${legacy_backup_dir}/node.yaml ]]; then
			rollback_node_yaml=${legacy_backup_dir}/node.yaml
		fi
	else
		[[ -f $owner_file && -d $backup_dir ]] || die "deployment backup is missing: ${state_dir}"
		rollback_kubelet_config=${backup_dir}/kubelet-config.yaml
		rollback_node_json=${backup_dir}/node.json
		[[ -f ${backup_dir}/had-standalone-dropin ]] &&
			rollback_dropin_file=${backup_dir}/20-standalone.conf
		[[ -f ${backup_dir}/had-standalone-config ]] &&
			rollback_standalone_file=${backup_dir}/standalone-config.yaml
	fi
	[[ -f $rollback_kubelet_config ]] || die "rollback Kubelet config is missing"
	[[ -f ${rollback_node_json:-/nonexistent} || -f ${rollback_node_yaml:-/nonexistent} ]] ||
		die "rollback Node metadata is missing"
}

restore_kubelet() {
	if [[ -n $rollback_dropin_dir ]]; then
		rm -f -- "${KUBELET_DROPIN}"
		install -d -m 755 "$(dirname -- "${KUBELET_DROPIN}")"
		local file
		for file in "${rollback_dropin_dir}"/*; do
			[[ -f $file ]] || continue
			install -m 644 "${file}" "$(dirname -- "${KUBELET_DROPIN}")/$(basename -- "${file}")"
		done
	elif [[ -n $rollback_dropin_file ]]; then
		install -m 644 "${rollback_dropin_file}" "${KUBELET_DROPIN}"
	else
		rm -f -- "${KUBELET_DROPIN}"
	fi
	if [[ -n $rollback_standalone_file ]]; then
		install -m 600 "${rollback_standalone_file}" "${STANDALONE_CONFIG}"
	fi
	install -m 644 "${rollback_kubelet_config}" "${KUBELET_CONFIG}"
	systemctl daemon-reload
	systemctl restart kubelet
	wait_until "registered Kubelet health" kubelet_healthy
	wait_until "Node registration" node_ready
	restore_node_metadata
	wait_until "Cilium Agent on restored node" cilium_pod_ready
	if [[ -z $rollback_standalone_file ]]; then
		rm -f -- "${STANDALONE_CONFIG}"
	fi
}

rollback_node() {
	for command_name in crictl curl flock ip jq kubectl systemctl; do
		require_command "${command_name}"
	done
	install -d -m 700 "${state_dir}"
	exec 9>"${state_dir}/lock"
	flock -n 9 || die "another operation is using ${state_dir}"
	resolve_rollback_backup
	stop_route_controller
	restore_kubelet
	ip -4 route flush table 254 proto "${ROUTE_PROTOCOL}"
	rm -rf -- "${ROUTE_CONFIG_DIR}"
	if [[ -z $legacy_backup_dir ]]; then
		write_phase rolled-back
	fi
	log "Kubelet registration restored and Route Controller removed"
}

check_rollback() {
	require_command kubectl
	require_command jq
	resolve_rollback_backup
	[[ -f $ROUTE_MANIFEST ]] || die "Route Controller manifest is missing"
	is_standalone || die "Kubelet is not in standalone mode"
	log "rollback backup and current standalone installation are valid"
}

delete_rbac() {
	kubectl delete -f "${bundle_dir}/deploy/route-controller-rbac.yaml" \
		--ignore-not-found --wait=true --timeout "${timeout}s"
	log "deleted Route Controller RBAC and token Secret"
}

case $action in
check)
	check_node_status
	;;
check-deploy)
	check_deploy
	;;
preflight-install)
	preflight_install
	;;
cluster-check)
	check_cluster
	;;
configure-cilium)
	configure_cilium
	;;
rollback-cilium)
	rollback_cilium
	;;
install)
	install_node
	;;
check-rollback)
	check_rollback
	;;
rollback)
	rollback_node
	;;
delete-rbac)
	delete_rbac
	;;
*)
	die "unknown action: ${action}"
	;;
esac
