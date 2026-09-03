#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  install-host-binary.sh --binary PATH --config PATH [options]

Options:
  --mode dry-run|active       Installation mode (default: dry-run)
  --sandbox-image IMAGE       Existing sandbox image (default: registry.k8s.io/pause:3.9)
  --manifest PATH             Static Pod destination
USAGE
}

binary_path=""
config_path=""
mode=dry-run
sandbox_image=registry.k8s.io/pause:3.9
manifest_path=/etc/kubernetes/manifests/route-controller.yaml

while (($# > 0)); do
  case "$1" in
    --binary) binary_path=${2:-}; shift 2 ;;
    --config) config_path=${2:-}; shift 2 ;;
    --mode) mode=${2:-}; shift 2 ;;
    --sandbox-image) sandbox_image=${2:-}; shift 2 ;;
    --manifest) manifest_path=${2:-}; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ${EUID} -ne 0 ]]; then
  echo "this installer must run as root" >&2
  exit 1
fi
if [[ ! -x "${binary_path}" ]]; then
  echo "binary does not exist or is not executable: ${binary_path}" >&2
  exit 1
fi
if [[ ! -s "${config_path}" ]]; then
  echo "configuration does not exist or is empty: ${config_path}" >&2
  exit 1
fi
case "${mode}" in
  dry-run) dry_run_argument=--dry-run=true ;;
  active) dry_run_argument=--dry-run=false ;;
  *) echo "mode must be dry-run or active" >&2; exit 2 ;;
esac
if [[ "${sandbox_image}" == *"|"* ]]; then
  echo "sandbox image must not contain |" >&2
  exit 2
fi

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
readonly template_path="${script_dir}/../static-pod/host-binary.yaml"
readonly install_dir=/etc/kubernetes/route-controller
readonly kubeconfig_path="${install_dir}/kubeconfig"

if [[ ! -s "${kubeconfig_path}" ]]; then
  echo "missing ${kubeconfig_path}; run bootstrap-kubeconfig.sh first" >&2
  exit 1
fi

for command_name in awk install mktemp mv sed sha256sum; do
  command -v "${command_name}" >/dev/null || {
    echo "required command is missing: ${command_name}" >&2
    exit 1
  }
done

install -d -m 700 "${install_dir}"
install -m 755 "${binary_path}" "${install_dir}/route-controller.next"
install -m 600 "${config_path}" "${install_dir}/config.yaml.next"
mv -f "${install_dir}/route-controller.next" "${install_dir}/route-controller"
mv -f "${install_dir}/config.yaml.next" "${install_dir}/config.yaml"

temporary_manifest=$(mktemp)
trap 'rm -f -- "${temporary_manifest}"' EXIT
binary_checksum=$(sha256sum "${binary_path}" | awk '{print $1}')
sed \
  -e "s|BINARY_CHECKSUM|${binary_checksum}|" \
  -e "s|SANDBOX_IMAGE|${sandbox_image}|" \
  -e "s|DRY_RUN_ARGUMENT|${dry_run_argument}|" \
  "${template_path}" >"${temporary_manifest}"
install -m 600 "${temporary_manifest}" "${manifest_path}.next"
mv -f "${manifest_path}.next" "${manifest_path}"

echo "installed ${manifest_path} in ${mode} mode"
