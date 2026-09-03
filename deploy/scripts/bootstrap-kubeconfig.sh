#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly script_dir
readonly default_rbac_path="${script_dir}/../rbac/rbac.yaml"
readonly output_path=${1:-/etc/kubernetes/route-controller/kubeconfig}
readonly api_server=${ROUTE_CONTROLLER_API_SERVER:-https://127.0.0.1:6443}
readonly tls_server_name=${ROUTE_CONTROLLER_TLS_SERVER_NAME:-apiserver.cluster.local}
readonly wait_timeout=${ROUTE_CONTROLLER_TOKEN_WAIT_TIMEOUT:-60}
readonly secret_name=route-controller-token
readonly namespace=kube-system

for command_name in kubectl base64 install mktemp; do
  command -v "${command_name}" >/dev/null || {
    echo "required command is missing: ${command_name}" >&2
    exit 1
  }
done

kubectl apply -f "${default_rbac_path}"

deadline=$((SECONDS + wait_timeout))
token_base64=""
ca_base64=""
while ((SECONDS < deadline)); do
  secret_data=$(kubectl -n "${namespace}" get secret "${secret_name}" \
    -o jsonpath='{.data.token}{" "}{.data.ca\.crt}' 2>/dev/null || true)
  read -r token_base64 ca_base64 <<<"${secret_data}"
  if [[ -n "${token_base64}" && -n "${ca_base64}" ]]; then
    break
  fi
  sleep 1
done
if [[ -z "${token_base64}" || -z "${ca_base64}" ]]; then
  echo "timed out after ${wait_timeout}s waiting for the ServiceAccount token Secret" >&2
  exit 1
fi

temporary_dir=$(mktemp -d)
trap 'rm -rf -- "${temporary_dir}"' EXIT
printf '%s' "${ca_base64}" | base64 --decode >"${temporary_dir}/ca.crt"
route_token=$(printf '%s' "${token_base64}" | base64 --decode)

install -d -m 700 "$(dirname -- "${output_path}")"
kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-cluster local-apiserver \
  --server="${api_server}" \
  --tls-server-name="${tls_server_name}" \
  --certificate-authority="${temporary_dir}/ca.crt" \
  --embed-certs=true >/dev/null
kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-credentials route-controller \
  --token="${route_token}" >/dev/null
kubectl config --kubeconfig="${temporary_dir}/kubeconfig" set-context route-controller \
  --cluster=local-apiserver \
  --user=route-controller >/dev/null
kubectl config --kubeconfig="${temporary_dir}/kubeconfig" use-context route-controller >/dev/null
install -m 600 "${temporary_dir}/kubeconfig" "${output_path}"

echo "wrote ${output_path}"
