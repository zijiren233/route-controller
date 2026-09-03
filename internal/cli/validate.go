package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/zijiren233/route-controller/internal/config"
	"sigs.k8s.io/yaml"
)

type outputFormat string

const (
	outputText outputFormat = "text"
	outputJSON outputFormat = "json"
	outputYAML outputFormat = "yaml"
)

func newValidateCommand() *cobra.Command {
	var format string

	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate and normalize configuration without contacting Kubernetes",
		Args:  cobra.NoArgs,
	}

	loader, err := config.NewLoader(command)
	if err != nil {
		panic(err)
	}

	command.Flags().
		StringVarP(&format, "output", "o", string(outputText), "output format: text, json, or yaml")
	command.RunE = func(command *cobra.Command, _ []string) error {
		loaded, err := loader.Load(command)
		if err != nil {
			return err
		}

		validated, err := loaded.Validate()
		if err != nil {
			return err
		}

		return writeValidation(command.OutOrStdout(), outputFormat(format), validated.Config)
	}

	return command
}

func writeValidation(writer io.Writer, format outputFormat, cfg config.Config) error {
	switch format {
	case outputText:
		return writeLine(writer, "configuration is valid")
	case outputJSON:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")

		if err := encoder.Encode(serializableConfig(cfg)); err != nil {
			return fmt.Errorf("encode JSON configuration: %w", err)
		}

		return nil
	case outputYAML:
		content, err := yaml.Marshal(serializableConfig(cfg))
		if err != nil {
			return fmt.Errorf("encode YAML configuration: %w", err)
		}

		if _, err := writer.Write(content); err != nil {
			return fmt.Errorf("write YAML configuration: %w", err)
		}

		return nil
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

func serializableConfig(cfg config.Config) map[string]any {
	return map[string]any{
		"kubernetes": map[string]any{
			"kubeconfig": cfg.Kubernetes.Kubeconfig,
			"qps":        cfg.Kubernetes.QPS,
			"burst":      cfg.Kubernetes.Burst,
		},
		"routes": map[string]any{
			"interface":    cfg.Routes.Interface,
			"pod-cidr":     cfg.Routes.PodCIDR,
			"service-cidr": cfg.Routes.ServiceCIDR,
			"router-cidr":  cfg.Routes.RouterCIDR,
			"table":        cfg.Routes.Table,
			"protocol":     cfg.Routes.Protocol,
		},
		"controller": map[string]any{
			"reconcile-period": cfg.Controller.ReconcilePeriod.String(),
			"dry-run":          cfg.Controller.DryRun,
		},
		"probe": map[string]any{
			"port":              cfg.Probe.Port,
			"timeout":           cfg.Probe.Timeout.String(),
			"failure-threshold": cfg.Probe.FailureThreshold,
			"success-threshold": cfg.Probe.SuccessThreshold,
		},
		"observability": map[string]any{
			"metrics-bind-address":      cfg.Observability.MetricsBindAddress,
			"health-probe-bind-address": cfg.Observability.HealthProbeBindAddress,
		},
		"logging": map[string]any{
			"level":  cfg.Logging.Level,
			"format": cfg.Logging.Format,
		},
	}
}
