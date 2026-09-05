package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zijiren233/route-controller/internal/config"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	validated, err := cfg.Validate()
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.0/10", validated.PodCIDR.String())
	assert.Equal(t, "10.192.0.0/12", validated.ServiceCIDR.String())
	assert.Equal(t, "192.0.2.0/24", validated.RouterCIDR.String())
}

func TestValidateNormalizesCIDRs(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	cfg.Routes.PodCIDR = "10.0.1.1/16"
	cfg.Routes.ServiceCIDR = "10.192.1.1/12"
	cfg.Routes.RouterCIDR = "192.0.2.17/24"

	validated, err := cfg.Validate()
	require.NoError(t, err)

	assert.Equal(t, "10.0.0.0/16", validated.Routes.PodCIDR)
	assert.Equal(t, "10.192.0.0/12", validated.Routes.ServiceCIDR)
	assert.Equal(t, "192.0.2.0/24", validated.Routes.RouterCIDR)
}

func TestValidateReportsAllImportantErrors(t *testing.T) {
	t.Parallel()

	cfg := validConfig()
	cfg.Routes.PodCIDR = "10.0.0.0/8"
	cfg.Routes.ServiceCIDR = "10.192.0.0/12"
	cfg.Routes.RouterCIDR = "10.1.0.0/16"
	cfg.Routes.Protocol = 0
	cfg.Probe.Timeout = 0
	cfg.Logging.Level = "verbose"

	_, err := cfg.Validate()
	require.Error(t, err)
	assert.ErrorContains(t, err, "routes.protocol")
	assert.ErrorContains(t, err, "probe.timeout")
	assert.ErrorContains(t, err, "logging.level")
	assert.ErrorContains(t, err, "routes.pod-cidr and routes.service-cidr overlap")
	assert.ErrorContains(t, err, "routes.pod-cidr and routes.router-cidr overlap")
}

func TestLoaderPrecedence(t *testing.T) {
	t.Setenv("ROUTE_CONTROLLER_PROBE_PORT", "4242")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
routes:
  pod-cidr: 10.0.0.0/10
  service-cidr: 10.192.0.0/12
  router-cidr: 192.0.2.0/24
probe:
  port: 4241
  timeout: 3s
`), 0o600))

	command := &cobra.Command{Use: "test"}
	loader, err := config.NewLoader(command)
	require.NoError(t, err)
	require.NoError(t, command.Flags().Set("config", configPath))
	require.NoError(t, command.Flags().Set("probe-timeout", "4s"))

	loaded, err := loader.Load(command)
	require.NoError(t, err)
	assert.Equal(t, 4242, loaded.Probe.Port, "environment overrides the configuration file")
	assert.Equal(
		t,
		4*time.Second,
		loaded.Probe.Timeout,
		"an explicit flag overrides the configuration file",
	)
}

func validConfig() config.Config {
	cfg := config.Defaults()
	cfg.Routes.Interface = "eth0"
	cfg.Routes.PodCIDR = "10.0.0.0/10"
	cfg.Routes.ServiceCIDR = "10.192.0.0/12"
	cfg.Routes.RouterCIDR = "192.0.2.0/24"

	return cfg
}
