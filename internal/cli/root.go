// Package cli defines the route-controller command line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/zijiren233/route-controller/internal/config"
	"github.com/zijiren233/route-controller/internal/logging"
	versioninfo "github.com/zijiren233/route-controller/internal/version"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type Runner func(context.Context, config.Config) error

func NewRootCommand(runner Runner) *cobra.Command {
	root := &cobra.Command{
		Use:           "route-controller",
		Short:         "Manage standalone control-plane routes through healthy Cilium workers",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       versioninfo.Get().Version,
	}
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.AddCommand(newRunCommand(runner), newValidateCommand(), newVersionCommand())

	return root
}

func Execute(runner Runner) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return NewRootCommand(runner).ExecuteContext(ctx)
}

func newRunCommand(runner Runner) *cobra.Command {
	command := &cobra.Command{
		Use:   "run",
		Short: "Run the route controller",
		Args:  cobra.NoArgs,
	}

	loader, err := config.NewLoader(command)
	if err != nil {
		panic(err)
	}

	command.RunE = func(command *cobra.Command, _ []string) error {
		loaded, err := loader.Load(command)
		if err != nil {
			return err
		}

		logger, err := logging.New(loaded.Logging)
		if err != nil {
			return err
		}

		ctrl.SetLogger(logger)
		ctx := log.IntoContext(command.Context(), logger)

		return runner(ctx, loaded)
	}

	return command
}

func writeLine(writer io.Writer, value string) error {
	if _, err := fmt.Fprintln(writer, value); err != nil {
		return fmt.Errorf("write command output: %w", err)
	}
	return nil
}
