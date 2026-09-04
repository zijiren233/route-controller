package cli_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zijiren233/route-controller/internal/cli"
	"github.com/zijiren233/route-controller/internal/config"
)

func TestValidateCommand(t *testing.T) {
	t.Parallel()

	command := cli.NewRootCommand(func(context.Context, config.Config) error { return nil })
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"validate",
		"--pod-cidr=10.0.0.0/10",
		"--service-cidr=10.192.0.0/12",
		"--router-cidr=192.0.2.0/24",
		"--interface=eth0",
	})

	require.NoError(t, command.Execute())
	assert.Equal(t, "configuration is valid\n", output.String())
}

func TestRunCommandReceivesConfigurationForRuntimeDiscovery(t *testing.T) {
	t.Parallel()

	var received config.Config

	command := cli.NewRootCommand(func(_ context.Context, cfg config.Config) error {
		received = cfg
		return nil
	})
	command.SetArgs([]string{
		"run",
		"--dry-run",
	})

	require.NoError(t, command.Execute())
	assert.True(t, received.Controller.DryRun)
	assert.Empty(t, received.Routes.PodCIDR)
	assert.Empty(t, received.Routes.ServiceCIDR)
	assert.Empty(t, received.Routes.RouterCIDR)
	assert.Empty(t, received.Routes.Interface)
}
