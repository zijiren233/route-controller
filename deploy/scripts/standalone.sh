#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly PROGRAM_NAME=standalone-kubelet
readonly KUBELET_CONFIG=/var/lib/kubelet/config.yaml
readonly STANDALONE_CONFIG=/var/lib/kubelet/standalone-config.yaml
readonly KUBELET_DROPIN=/etc/systemd/system/kubelet.service.d/20-standalone.conf
readonly ROUTE_CONFIG_DIR=/etc/kubernetes/route-controller
readonly ROUTE_KUBECONFIG=${ROUTE_CONFIG_DIR}/kubeconfig
readonly ROUTE_MANIFEST=/etc/kubernetes/manifests/route-controller.yaml
readonly OWNER=standalone-kubelet-reboot-v2

log() { printf '[%s] %s\n' "$PROGRAM_NAME" "$*"; }
die() {
	log "ERROR: $*" >&2
	exit 1
}
require_command() { command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"; }

usage() {
	cat <<'EOF'
Usage: deploy/scripts/standalone.sh COMMAND [options]
       deploy/scripts/standalone.sh help [COMMAND]
       deploy/scripts/standalone.sh COMMAND --help

Commands:
  deploy       Back up local files, stage standalone kubelet and Static Pod.
  rollback     Stop Controller, clear its routes, restore files, start kubelet.
  check        Check local kubelet/Controller after deploy or rollback.
  kubeconfig   Import/reuse Controller credentials, or generate via admin.

Common options:
  --kubeconfig FILE        Pre-generated Controller kubeconfig; otherwise reuse
                          /etc/kubernetes/route-controller/kubeconfig.
  --state-dir PATH        Backups (default /var/lib/standalone-kubelet-manager).
  --yes                   Confirm changes; required for non-interactive execution.
  -h, --help              Show help without root, dependencies, or API access.
Deploy / rollback:
  --image IMAGE@sha256:... Required for deploy. Use a successful Actions digest.
  --reboot                Deploy: reboot after staging, otherwise reboot manually.
                          Rollback: optional reboot instead of starting kubelet.
  --check                 Read-only local input/backup check, not cluster health.
Kubeconfig only:
  --admin-kubeconfig FILE  Used ONLY when generating; never needed for import.
                          Fallback: KUBECONFIG, /etc/kubernetes/admin.conf,
                          then ~/.kube/config.
  --output PATH           Absolute destination (default Controller path above).
  --api-server URL        Generated endpoint (default inherited from admin).
  --tls-server-name NAME  Generated TLS name (default inherited from admin).
  --timeout SECONDS       Token generation timeout (default 300).

RECOMMENDED DEPLOYMENT: ONE CONTROL PLANE AT A TIME
  Run from the repository root; adjacent deploy templates must be present.
  IMAGE='ghcr.io/zijiren233/route-controller@sha256:<64-hex-digest>'
  sudo deploy/scripts/standalone.sh deploy --check \
    --kubeconfig /secure/controller.conf --image "$IMAGE"
  sudo deploy/scripts/standalone.sh deploy \
    --kubeconfig /secure/controller.conf --image "$IMAGE" --yes --reboot
  # Reconnect after reboot:
  sudo deploy/scripts/standalone.sh check
  curl -fsS http://127.0.0.1:9919/readyz
  curl -fsS http://127.0.0.1:9918/status
  ip -4 route show table 254 proto 99

  Omit --reboot to inspect staged files, then run sudo systemctl reboot.
  Staging STOPS kubelet. Reboot promptly; do not start kubelet before reboot.
  deploy does not upgrade existing installations or replace their backups.
  Recover an unfinished staging operation using rollback.

NO ADMIN ON THE TARGET
  deploy, rollback, and check make no Kubernetes API writes, never discover
  admin credentials, and never delete Node, Lease, RBAC, or token resources.
  Only portable Controller credentials are required for deploy. If --kubeconfig
  is omitted, the existing destination is reused; missing credentials fail.
  Import embeds certificates and writes mode 0600. Exec/auth-provider plugins
  and tokenFile references are unsupported inside the Static Pod.
  The retained Node eventually becomes NotReady; labels/taints are preserved.
  Static/mirror and DaemonSet API objects may remain stale. An administrator
  can clean them externally; deleting the Node loses its rollback metadata.

ARRANGE THESE PREREQUISITES YOURSELF
  Migrate ordinary workloads; this script does not drain or inspect workloads.
  Use standard kubeadm/systemd layout, working CRI, and enough other healthy
  control planes for API/etcd quorum throughout the reboot.
  Configure Cilium external ClusterIP support and worker forwarding/rp_filter.
  Validate direct networking; remove incompatible EgressSelector/Konnectivity.
  Cilium settings, CNI files, sysctls, Helm releases, and API Server manifests
  are untouched. No cilium-dbg is copied or executed. Reboot clears kernel
  BPF/interfaces/routes; persistent Cilium/CNI files remain. Standalone Static
  Pods use host networking. Verify Cilium does not run again on this node.
  Standalone kubelet disables HTTPS, read-only HTTP, health HTTP, and debugging
  handlers. Local health checks use systemd and Controller readiness.
  Table 254 protocol 99 is reserved for this deployment; rollback flushes it.
  Tools: root, bash, kubectl, jq, systemctl, crictl, flock, curl, ip, coreutils.
  check verifies local health/routes; verify PodIP, ClusterIP, logs, exec, and
  port-forward before converting the next node.

ROLLBACK: NO REBOOT, API CONNECTIVITY, OR ADMIN REQUIRED
  sudo deploy/scripts/standalone.sh rollback --check
  sudo deploy/scripts/standalone.sh rollback --yes
  sudo deploy/scripts/standalone.sh check
  Stop kubelet, stop the Controller CRI container, flush IPv4 table 254 proto 99,
  restore files, daemon-reload, then start kubelet. Controller must stop before
  clearing routes so it cannot recreate them. Other Static Pods keep running.
  Original kubelet credentials/configuration rejoin the retained Node.
  Confirm Node and Cilium recovery separately. --reboot remains optional.
  Pre-existing files are restored; newly created files removed; RBAC untouched.
  Keep the same --state-dir. Old live-conversion backups require that older
  script's rollback first: that version deleted Node metadata.
  Keep backups after rollback; move the old state directory aside or select a
  new --state-dir before a fresh deployment.

CREDENTIAL EXAMPLES
  # Import (no admin or API request):
  sudo deploy/scripts/standalone.sh kubeconfig --kubeconfig /secure/controller.conf --yes
  # Reuse destination (no admin or API request):
  sudo deploy/scripts/standalone.sh kubeconfig --yes
  # Generate on a management machine, then securely transfer to the target:
  sudo deploy/scripts/standalone.sh kubeconfig --admin-kubeconfig /secure/admin.conf \
    --output /secure/output/controller.conf --api-server https://api.example.com:6443 \
    --tls-server-name api.example.com --yes
  Only generation creates shared RBAC/token. Generation inherits the admin
  endpoint/TLS name; import preserves the supplied endpoint and identity.
  Use a dedicated Controller identity with the repository's RBAC permissions.
  Existing output is reused; select a new --output to generate a fresh file.
  Tokens are long-lived: protect credentials and coordinate rotation.

FILES / STATUS
  /var/lib/kubelet/config.yaml                         Original, unchanged
  /var/lib/kubelet/standalone-config.yaml               Generated config
  /etc/systemd/system/kubelet.service.d/20-standalone.conf
  /etc/kubernetes/manifests/route-controller.yaml       Static Pod
  /etc/kubernetes/route-controller/kubeconfig           Credentials
  STATE/backup, STATE/owner, STATE/phase, STATE/boot-id  Local recovery state
  Deploy requires reboot and a successful check. Default rollback starts
  kubelet immediately. check verifies boot ID only for operations using reboot.
  Exit 0 means success; nonzero means failure. Errors retain backups; no
  automatic rollback or automatic kubelet restart is performed.
EOF
}

action=${1:-help}
(($# == 0)) || shift
if [[ $action == help ]]; then
	(($# <= 1)) || die "usage: standalone.sh help [COMMAND]"
	case ${1:-all} in all | deploy | rollback | check | kubeconfig)
		usage
		exit 0
		;;
	*) die "unknown help topic: $1" ;; esac
fi
case $action in -h | --help)
	usage
	exit 0
	;;
deploy | rollback | check | kubeconfig) ;; *) die "unknown command: $action" ;; esac
project_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
state_dir=/var/lib/standalone-kubelet-manager
controller_kubeconfig=
admin_kubeconfig=
output_path=$ROUTE_KUBECONFIG
route_image=
api_server=
tls_server_name=
timeout=300
assume_yes=0
reboot=0
check_only=0
temporary_dir=
render_dir=
while (($#)); do
	case $1 in
	--kubeconfig | --state-dir | --image | --admin-kubeconfig | --output | --api-server | --tls-server-name | --timeout)
		if (($# < 2)) || [[ -z $2 || $2 == --* ]]; then die "missing value for $1"; fi
		;;
	esac
	case $1 in
	--kubeconfig)
		controller_kubeconfig=$2
		shift 2
		;;
	--state-dir)
		state_dir=$2
		shift 2
		;;
	--image)
		route_image=$2
		shift 2
		;;
	--admin-kubeconfig)
		admin_kubeconfig=$2
		shift 2
		;;
	--output)
		output_path=$2
		shift 2
		;;
	--api-server)
		api_server=$2
		shift 2
		;;
	--tls-server-name)
		tls_server_name=$2
		shift 2
		;;
	--timeout)
		timeout=$2
		shift 2
		;;
	--yes)
		assume_yes=1
		shift
		;;
	--reboot)
		reboot=1
		shift
		;;
	--check)
		check_only=1
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown option: $1" ;;
	esac
done
[[ $EUID == 0 ]] || die "must run as root"
[[ $state_dir == /* && $output_path == /* ]] || die "state/output paths must be absolute"
[[ $timeout =~ ^[1-9][0-9]*$ ]] || die "timeout must be a positive integer"
if [[ $action != kubeconfig ]]; then
	[[ -z $admin_kubeconfig && -z $api_server && -z $tls_server_name && $output_path == "$ROUTE_KUBECONFIG" ]] ||
		die "credential generation options are only supported by kubeconfig"
fi
[[ $action == deploy || -z $route_image ]] || die "--image is only supported by deploy"
if [[ $action == check || $action == kubeconfig ]]; then
	((reboot == 0 && check_only == 0)) || die "--reboot/--check require deploy or rollback"
fi
readonly backup_dir=$state_dir/backup
cleanup() {
	[[ -z $temporary_dir ]] || rm -rf -- "$temporary_dir"
	[[ -z $render_dir ]] || rm -rf -- "$render_dir"
}
trap cleanup EXIT
confirm_changes() {
	((assume_yes)) && return
	[[ -t 0 ]] || die "non-interactive execution requires --yes"
	local answer
	read -r -p "Run $action on this machine (deploy/rollback stop kubelet)? [y/N] " answer
	[[ $answer == y || $answer == Y ]] || die "cancelled"
}
phase() { if [[ -f $state_dir/phase ]]; then cat "$state_dir/phase"; else printf none; fi; }
write_phase() {
	printf '%s\n' "$1" >"$state_dir/phase.next"
	mv -f "$state_dir/phase.next" "$state_dir/phase"
}
lock_state() {
	install -d -m 700 "$state_dir"
	exec 9>"$state_dir/lock"
	flock -n 9 || die "another operation uses $state_dir"
}
check_owner() {
	[[ -f $state_dir/owner && $(<"$state_dir/owner") == "$OWNER" ]] ||
		die "reboot-deployer backup missing; use the original deployer for older backups"
}
require_admin() {
	if [[ -z $admin_kubeconfig ]]; then
		if [[ -n ${KUBECONFIG:-} ]]; then
			admin_kubeconfig=$KUBECONFIG
		elif [[ -f /etc/kubernetes/admin.conf ]]; then
			admin_kubeconfig=/etc/kubernetes/admin.conf
		elif [[ -f $HOME/.kube/config ]]; then
			admin_kubeconfig=$HOME/.kube/config
		else
			die "generation requires --admin-kubeconfig; import pre-generated credentials instead"
		fi
	fi
	export KUBECONFIG=$admin_kubeconfig
}
create_route_kubeconfig() {
	local source=$controller_kubeconfig
	if [[ -z $source && -f $output_path ]]; then
		log "reusing existing Controller kubeconfig"
		source=$output_path
	fi
	if [[ -n $source ]]; then
		[[ -f $source ]] || die "Controller kubeconfig does not exist"
		log "importing Controller kubeconfig (local operation; no API request)"
		temporary_dir=$(mktemp -d)
		kubectl --kubeconfig="$source" config view --raw --flatten --minify -o json >"$temporary_dir/config"
		jq -e '(.users | length) == 1 and
            all(.users[].user; .exec == null and ."auth-provider" == null and .tokenFile == null)' \
			"$temporary_dir/config" >/dev/null ||
			die "Controller kubeconfig must contain portable credentials (no exec, auth-provider, or tokenFile)"
		mkdir -p "$(dirname -- "$output_path")"
		install -m 600 "$temporary_dir/config" "$output_path"
		rm -rf -- "$temporary_dir"
		temporary_dir=
		log "imported Controller kubeconfig"
		return
	fi
	require_admin
	generate_route_kubeconfig
}

generate_route_kubeconfig() {
	local secret_data token_base64 ca_base64 route_token deadline
	local cluster_config
	cluster_config=$(kubectl config view --minify -o json)
	[[ -n $api_server ]] || api_server=$(jq -er '.clusters[0].cluster.server' <<<"$cluster_config")
	[[ -n $tls_server_name ]] || tls_server_name=$(jq -r '.clusters[0].cluster["tls-server-name"] // ""' <<<"$cluster_config")
	log "applying Controller RBAC, ServiceAccount, and token Secret"
	kubectl apply -f "${project_dir}/deploy/rbac/rbac.yaml"
	log "waiting for ServiceAccount token (timeout: ${timeout}s)"
	deadline=$((SECONDS + timeout))
	token_base64=
	ca_base64=
	until [[ -n $token_base64 && -n $ca_base64 ]]; do
		((SECONDS < deadline)) || die "timed out waiting for Route Controller token"
		secret_data=$(kubectl -n kube-system get secret route-controller-token \
			-o jsonpath='{.data.token}{" "}{.data.ca\.crt}' 2>/dev/null || true)
		read -r token_base64 ca_base64 <<<"${secret_data}" || true
		[[ -n $token_base64 && -n $ca_base64 ]] || sleep 1
	done
	[[ -n $token_base64 && -n $ca_base64 ]] || die "Route Controller token Secret is incomplete"
	log "token received; writing Controller kubeconfig"
	temporary_dir=$(mktemp -d)
	printf '%s' "${ca_base64}" | base64 --decode >"${temporary_dir}/ca.crt"
	route_token=$(printf '%s' "${token_base64}" | base64 --decode)
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-cluster local-apiserver \
		--server="${api_server}" \
		--tls-server-name="${tls_server_name}" \
		--certificate-authority="${temporary_dir}/ca.crt" \
		--embed-certs=true >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-credentials route-controller \
		--token="${route_token}" >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-context route-controller \
		--cluster=local-apiserver --user=route-controller >/dev/null
	kubectl config --kubeconfig="${temporary_dir}/kubeconfig" use-context route-controller >/dev/null
	mkdir -p "$(dirname -- "$output_path")"
	install -m 600 "${temporary_dir}/kubeconfig" "$output_path"
	rm -rf -- "${temporary_dir}"
	temporary_dir=
	log "generated Controller kubeconfig"

}

backup_files() {
	local file
	mkdir "$backup_dir"
	for file in "$STANDALONE_CONFIG" "$KUBELET_DROPIN" "$ROUTE_MANIFEST" "$ROUTE_KUBECONFIG"; do
		if [[ -e $file ]]; then
			mkdir -p "$backup_dir$(dirname "$file")"
			cp -p -- "$file" "$backup_dir$file"
		fi
	done
	touch "$backup_dir/complete"
}
restore_files() {
	local file
	for file in "$STANDALONE_CONFIG" "$KUBELET_DROPIN" "$ROUTE_MANIFEST" "$ROUTE_KUBECONFIG"; do
		if [[ -f $backup_dir$file ]]; then
			mkdir -p "$(dirname "$file")"
			cp -p -- "$backup_dir$file" "$file"
		else
			rm -f -- "$file"
		fi
	done
}
check_deploy_inputs() {
	local file
	for file in "$KUBELET_CONFIG" "$project_dir/deploy/standalone/kubelet-standalone-merge.json" \
		"$project_dir/deploy/standalone/20-standalone.conf" "$project_dir/deploy/static-pod/image.yaml"; do
		[[ -f $file ]] || die "required file missing: $file"
	done
	[[ $route_image =~ ^[A-Za-z0-9._/:@+-]+@sha256:[a-f0-9]{64}$ ]] ||
		die "--image must use an immutable sha256 digest"
	[[ -f ${controller_kubeconfig:-$ROUTE_KUBECONFIG} ]] ||
		die "provide --kubeconfig or prepare $ROUTE_KUBECONFIG first"
	[[ ! -e $state_dir/owner && ! -e $backup_dir ]] ||
		die "existing deployment state; use check or rollback before deploying again"
	[[ ! -e $KUBELET_DROPIN && ! -e $ROUTE_MANIFEST ]] ||
		die "existing standalone installation; restore it using its original deployer first"
}
check_rollback_inputs() {
	check_owner
	[[ -f $backup_dir/complete ]] || die "local backup is incomplete"
}
finish_staging() {
	log "reloading systemd configuration"
	systemctl daemon-reload
	log "files staged; kubelet is stopped until reboot"
	if ((reboot)); then
		log "requesting node reboot; reconnect and run check"
		systemctl reboot
	else
		log "run systemctl reboot, then reconnect and run check"
	fi
}
deploy() {
	log "checking local deployment inputs"
	check_deploy_inputs
	confirm_changes
	lock_state
	check_deploy_inputs
	render_dir=$(mktemp -d)
	# Render and pull before changing the running kubelet.
	output_path=$render_dir/kubeconfig
	controller_kubeconfig=${controller_kubeconfig:-$ROUTE_KUBECONFIG}
	create_route_kubeconfig
	log "rendering standalone kubelet configuration and Controller Static Pod"
	kubectl patch --local -f "$KUBELET_CONFIG" --type=merge \
		--patch-file "$project_dir/deploy/standalone/kubelet-standalone-merge.json" -o yaml \
		>"$render_dir/standalone-config.yaml"
	kubectl set image --local -f "$project_dir/deploy/static-pod/image.yaml" \
		"controller=$route_image" -o yaml >"$render_dir/route-controller.yaml"
	log "pulling Controller image: $route_image"
	log "image pull may take several minutes; kubelet has not been stopped"
	local pull_started=$SECONDS
	crictl pull "$route_image" >/dev/null
	log "Controller image ready (elapsed: $((SECONDS - pull_started))s)"
	log "backing up local files to $backup_dir"
	backup_files
	printf '%s\n' "$OWNER" >"$state_dir/owner"
	cat /proc/sys/kernel/random/boot_id >"$state_dir/boot-id"
	write_phase staging-deploy
	log "stopping kubelet"
	systemctl stop kubelet
	log "installing standalone configuration, credentials, and Static Pod manifest"
	install -m 600 "$render_dir/standalone-config.yaml" "$STANDALONE_CONFIG"
	install -d -m 755 "$(dirname "$KUBELET_DROPIN")"
	install -m 644 "$project_dir/deploy/standalone/20-standalone.conf" "$KUBELET_DROPIN"
	mkdir -p "$ROUTE_CONFIG_DIR"
	install -m 600 "$output_path" "$ROUTE_KUBECONFIG"
	install -m 600 "$render_dir/route-controller.yaml" "$ROUTE_MANIFEST"
	write_phase pending-deploy-reboot
	finish_staging
}
rollback() {
	log "checking local rollback backup"
	check_rollback_inputs
	confirm_changes
	lock_state
	check_rollback_inputs
	cat /proc/sys/kernel/random/boot_id >"$state_dir/boot-id"
	write_phase staging-rollback
	log "stopping kubelet"
	systemctl stop kubelet
	# Stopping kubelet alone leaves its containers running and routes reconciling.
	local containers container
	log "locating Controller containers in the local CRI runtime"
	containers=$(crictl ps -o json | jq -er '[.containers[] |
        select(.metadata.name == "controller" and
            .labels["io.kubernetes.pod.namespace"] == "kube-system" and
            (.labels["io.kubernetes.pod.name"] // "" | startswith("route-controller-"))) |
        .id] | join("\n")')
	while IFS= read -r container; do
		if [[ -n $container ]]; then
			log "stopping Controller container: $container"
			crictl stop "$container" >/dev/null
		fi
	done <<<"$containers"
	log "clearing Controller IPv4 routes (table 254, protocol 99)"
	ip -4 route flush table 254 proto 99
	log "restoring local files from $backup_dir"
	restore_files
	if ((reboot)); then
		write_phase pending-rollback-reboot
		finish_staging
	else
		log "reloading systemd configuration"
		systemctl daemon-reload
		log "starting original kubelet"
		systemctl start kubelet
		write_phase rolled-back
		log "files restored and kubelet started; verify Node/Cilium recovery"
	fi
}
check() {
	log "checking local deployment state and kubelet service"
	check_owner
	local current_phase running_cilium
	current_phase=$(phase)
	case $current_phase in
	pending-deploy-reboot | pending-rollback-reboot)
		[[ $(<"$state_dir/boot-id") != "$(</proc/sys/kernel/random/boot_id)" ]] ||
			die "node has not rebooted since staging"
		;;
	rolled-back) ;;
	*) die "incomplete staging ($current_phase); use rollback" ;;
	esac
	systemctl is-active --quiet kubelet || die "kubelet is not active"
	if [[ $current_phase == pending-deploy-reboot ]]; then
		[[ -f $KUBELET_DROPIN && -f $ROUTE_MANIFEST && -f $ROUTE_KUBECONFIG ]] ||
			die "standalone files are missing"
		log "checking local CRI containers"
		running_cilium=$(crictl ps --quiet --name cilium) || die "cannot inspect local CRI containers"
		[[ -z $running_cilium ]] || die "Cilium is still running locally"
		log "checking Controller readiness (timeout: 5s) and managed routes"
		curl -fsS --max-time 5 http://127.0.0.1:9919/readyz >/dev/null ||
			die "Controller is not ready"
		[[ -n $(ip -4 route show table 254 proto 99) ]] || die "managed routes are missing"
		log "standalone kubelet and Controller are healthy after reboot"
	else
		log "restored kubelet is active; verify Node/Cilium recovery"
	fi
}
case $action in
deploy)
	for command_name in kubectl jq crictl systemctl flock; do require_command "$command_name"; done
	if ((check_only)); then
		check_deploy_inputs
		log "local deployment inputs are present"
	else deploy; fi
	;;
rollback)
	for command_name in systemctl flock crictl jq ip; do require_command "$command_name"; done
	if ((check_only)); then
		check_rollback_inputs
		log "local rollback backup is present"
	else rollback; fi
	;;
check)
	for command_name in systemctl curl crictl ip; do require_command "$command_name"; done
	check
	;;
kubeconfig)
	for command_name in kubectl jq base64; do require_command "$command_name"; done
	confirm_changes
	create_route_kubeconfig
	;;
esac
