package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	versioninfo "github.com/zijiren233/route-controller/internal/version"
	"sigs.k8s.io/yaml"
)

func newVersionCommand() *cobra.Command {
	var format string

	command := &cobra.Command{
		Use:   "version",
		Short: "Print build version information",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			info := versioninfo.Get()
			switch outputFormat(format) {
			case outputText:
				return writeLine(
					command.OutOrStdout(),
					fmt.Sprintf(
						"route-controller %s (commit %s, built %s, %s, %s)",
						info.Version,
						info.GitCommit,
						info.BuildDate,
						info.GoVersion,
						info.Platform,
					),
				)
			case outputJSON:
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")

				if err := encoder.Encode(info); err != nil {
					return fmt.Errorf("encode version JSON: %w", err)
				}

				return nil
			case outputYAML:
				content, err := yaml.Marshal(info)
				if err != nil {
					return fmt.Errorf("encode version YAML: %w", err)
				}

				if _, err := command.OutOrStdout().Write(content); err != nil {
					return fmt.Errorf("write version YAML: %w", err)
				}

				return nil
			default:
				return fmt.Errorf("unsupported output format %q", format)
			}
		},
	}
	command.Flags().
		StringVarP(&format, "output", "o", string(outputText), "output format: text, json, or yaml")

	return command
}
