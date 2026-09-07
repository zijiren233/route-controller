//go:build integration && linux

package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStandaloneRebootLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("mount namespace test requires root")
	}
	if err := exec.Command("unshare", "--mount", "--propagation", "private", "true").Run(); err != nil {
		t.Skipf("private mount namespace unavailable: %v", err)
	}
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	dir := t.TempDir()
	for _, path := range []string{"bin", "etc/kubernetes/manifests", "var/kubelet"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, path), 0o700))
	}
	mock := `#!/bin/bash
set -eu
printf '%s %s\n' "${0##*/}" "$*" >>"$TEST_DIR/calls"
case ${0##*/} in
kubectl)
  if [[ $* == *"config view --raw --flatten --minify"* ]]; then
    printf '%s\n' '{"users":[{"user":{"token":"test"}}]}'
  elif [[ $* == "patch --local "* ]]; then
    printf '%s\n' 'registerNode: false'
  elif [[ $* == "set image --local "* ]]; then
    printf '%s\n' 'kind: Pod'
  else
    echo "unexpected API call: $*" >&2
    exit 1
  fi ;;
crictl)
  if [[ $1 == pull && -f $TEST_DIR/fail-pull ]]; then exit 1; fi ;;
systemctl)
  if [[ $1 == stop && -f $TEST_DIR/fail-stop ]]; then exit 1; fi ;;
curl) ;;
ip) echo '10.0.0.0/24 via 192.0.2.1' ;;
esac
`
	for _, name := range []string{"kubectl", "crictl", "systemctl", "curl", "ip"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", name), []byte(mock), 0o700))
	}
	runner := `set -Eeuo pipefail
# All absolute deployment paths are isolated from the host in this namespace.
mount --bind "$TEST_DIR/etc" /etc
mount --bind "$TEST_DIR/var" /var/lib
printf 'boot-before\n' >"$TEST_DIR/boot-id"
mount --bind "$TEST_DIR/boot-id" /proc/sys/kernel/random/boot_id
export PATH="$TEST_DIR/bin:$PATH"
export KUBECONFIG=/nonexistent/admin.conf
script="$REPO/deploy/scripts/standalone.sh"
image="example.invalid/controller@sha256:$(printf '%064d' 0)"
state="$TEST_DIR/state"
printf 'original-kubelet\n' >/var/lib/kubelet/config.yaml
printf 'controller-input\n' >"$TEST_DIR/controller.conf"
test ! -e /etc/kubernetes/admin.conf

# Preflight is read-only and a failed pull must not stop kubelet or change files.
bash "$script" deploy --check --state-dir "$state" --image "$image" --kubeconfig "$TEST_DIR/controller.conf"
test ! -e "$state"
touch "$TEST_DIR/fail-pull"
if bash "$script" deploy --state-dir "$state" --image "$image" --kubeconfig "$TEST_DIR/controller.conf" --yes; then exit 1; fi
test ! -e /var/lib/kubelet/standalone-config.yaml
! grep -q '^systemctl stop' "$TEST_DIR/calls"
rm "$TEST_DIR/fail-pull"

# Stage using Controller credentials only; no reboot unless explicitly requested.
bash "$script" deploy --state-dir "$state" --image "$image" --kubeconfig "$TEST_DIR/controller.conf" --yes
test -f /etc/kubernetes/manifests/route-controller.yaml
test "$(stat -c %a /etc/kubernetes/route-controller/kubeconfig)" = 600
test "$(cat /var/lib/kubelet/config.yaml)" = original-kubelet
! grep -q '^systemctl reboot' "$TEST_DIR/calls"
if bash "$script" check --state-dir "$state"; then exit 1; fi
printf 'boot-deployed\n' >"$TEST_DIR/boot-id"
bash "$script" check --state-dir "$state"
if bash "$script" deploy --state-dir "$state" --image "$image" --yes; then exit 1; fi

# Rollback restores local state without API access and requires another reboot.
bash "$script" rollback --check --state-dir "$state"
bash "$script" rollback --state-dir "$state" --yes --reboot
test ! -e /etc/kubernetes/manifests/route-controller.yaml
test ! -e /etc/kubernetes/route-controller/kubeconfig
test ! -e /var/lib/kubelet/standalone-config.yaml
test ! -e /etc/systemd/system/kubelet.service.d/20-standalone.conf
grep -q '^systemctl reboot' "$TEST_DIR/calls"
if bash "$script" check --state-dir "$state"; then exit 1; fi
printf 'boot-restored\n' >"$TEST_DIR/boot-id"
bash "$script" check --state-dir "$state"

# Preserve a pre-existing Controller kubeconfig, including after interrupted staging.
printf 'existing-credentials\n' >/etc/kubernetes/route-controller/kubeconfig
state="$TEST_DIR/interrupted"
touch "$TEST_DIR/fail-stop"
if bash "$script" deploy --state-dir "$state" --image "$image" --yes; then exit 1; fi
rm "$TEST_DIR/fail-stop"
bash "$script" rollback --state-dir "$state" --yes
test "$(cat /etc/kubernetes/route-controller/kubeconfig)" = existing-credentials
test "$(cat /var/lib/kubelet/config.yaml)" = original-kubelet
`
	cmd := exec.Command("unshare", "--mount", "--propagation", "private", "bash", "-c", runner)
	cmd.Env = append(os.Environ(), "TEST_DIR="+dir, "REPO="+repo)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
}
