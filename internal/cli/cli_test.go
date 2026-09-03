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

	command := cli.NewRootCommand(func(context.Context, config.Validated) error { return nil })
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"validate",
		"--pod-cidr=10.0.0.0/10",
		"--service-cidr=10.192.0.0/12",
		"--router-cidr=192.0.2.0/24",
	})

	require.NoError(t, command.Execute())
	assert.Equal(t, "configuration is valid\n", output.String())
}

func TestRunCommandReceivesValidatedConfiguration(t *testing.T) {
	t.Parallel()

	var received config.Validated

	command := cli.NewRootCommand(func(_ context.Context, cfg config.Validated) error {
		received = cfg
		return nil
	})
	command.SetArgs([]string{
		"run",
		"--pod-cidr=10.0.0.0/10",
		"--service-cidr=10.192.0.0/12",
		"--router-cidr=192.0.2.0/24",
		"--dry-run",
	})

	require.NoError(t, command.Execute())
	assert.True(t, received.Controller.DryRun)
	assert.Equal(t, "10.0.0.0/10", received.PodCIDR.String())
}
