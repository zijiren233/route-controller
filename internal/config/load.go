package config

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const environmentPrefix = "ROUTE_CONTROLLER"

type Loader struct {
	values *viper.Viper
}

func NewLoader(command *cobra.Command) (*Loader, error) {
	values := viper.New()
	values.SetEnvPrefix(environmentPrefix)
	values.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	values.AutomaticEnv()
	setDefaults(values)

	if err := addFlags(command.Flags(), values); err != nil {
		return nil, err
	}

	return &Loader{values: values}, nil
}

func (loader *Loader) Load(command *cobra.Command) (Config, error) {
	configPath, err := command.Flags().GetString("config")
	if err != nil {
		return Config{}, fmt.Errorf("read --config: %w", err)
	}

	if configPath != "" {
		loader.values.SetConfigFile(configPath)

		if err := loader.values.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read configuration file %q: %w", configPath, err)
		}
	}

	return Config{
		Kubernetes: KubernetesConfig{
			Kubeconfig: loader.values.GetString("kubernetes.kubeconfig"),
			QPS:        float32(loader.values.GetFloat64("kubernetes.qps")),
			Burst:      loader.values.GetInt("kubernetes.burst"),
		},
		Routes: RoutesConfig{
			Interface:   loader.values.GetString("routes.interface"),
			PodCIDR:     loader.values.GetString("routes.pod-cidr"),
			ServiceCIDR: loader.values.GetString("routes.service-cidr"),
			RouterCIDR:  loader.values.GetString("routes.router-cidr"),
			Table:       loader.values.GetInt("routes.table"),
			Protocol:    loader.values.GetInt("routes.protocol"),
		},
		Controller: ControllerConfig{
			ReconcilePeriod: loader.values.GetDuration("controller.reconcile-period"),
			DryRun:          loader.values.GetBool("controller.dry-run"),
		},
		Probe: ProbeConfig{
			Port:             loader.values.GetInt("probe.port"),
			Timeout:          loader.values.GetDuration("probe.timeout"),
			FailureThreshold: loader.values.GetInt("probe.failure-threshold"),
			SuccessThreshold: loader.values.GetInt("probe.success-threshold"),
		},
		Observability: ObservabilityConfig{
			MetricsBindAddress: loader.values.GetString("observability.metrics-bind-address"),
			HealthProbeBindAddress: loader.values.GetString(
				"observability.health-probe-bind-address",
			),
		},
		Logging: LoggingConfig{
			Level:  LogLevel(loader.values.GetString("logging.level")),
			Format: LogFormat(loader.values.GetString("logging.format")),
		},
	}, nil
}

func addFlags(flags *pflag.FlagSet, values *viper.Viper) error {
	defaults := Defaults()

	flags.String("config", "", "path to a YAML configuration file")
	flags.String(
		"kubeconfig",
		defaults.Kubernetes.Kubeconfig,
		"path to the read-only Kubernetes kubeconfig",
	)
	flags.Float32(
		"kubernetes-qps",
		defaults.Kubernetes.QPS,
		"Kubernetes client requests per second",
	)
	flags.Int("kubernetes-burst", defaults.Kubernetes.Burst, "Kubernetes client burst limit")
	flags.String(
		"interface",
		defaults.Routes.Interface,
		"host interface used to reach worker next hops (automatically discovered when empty)",
	)
	flags.String("pod-cidr", "", "allowed cluster Pod CIDR (automatically discovered when empty)")
	flags.String("service-cidr", "", "cluster Service CIDR (automatically discovered when empty)")
	flags.String(
		"router-cidr",
		"",
		"allowed worker node IP CIDR (automatically discovered when empty)",
	)
	flags.Int("route-table", defaults.Routes.Table, "Linux route table number")
	flags.Int(
		"route-protocol",
		defaults.Routes.Protocol,
		"Linux route protocol owned by this controller",
	)
	flags.Duration(
		"reconcile-period",
		defaults.Controller.ReconcilePeriod,
		"full reconciliation interval",
	)
	flags.Bool(
		"dry-run",
		defaults.Controller.DryRun,
		"calculate and report route changes without applying them",
	)
	flags.Int("probe-port", defaults.Probe.Port, "Cilium health endpoint port")
	flags.Duration(
		"probe-timeout",
		defaults.Probe.Timeout,
		"Cilium health endpoint request timeout",
	)
	flags.Int(
		"probe-failure-threshold",
		defaults.Probe.FailureThreshold,
		"failures before withdrawing a Service next hop",
	)
	flags.Int(
		"probe-success-threshold",
		defaults.Probe.SuccessThreshold,
		"successes before adding a Service next hop",
	)
	flags.String(
		"metrics-bind-address",
		defaults.Observability.MetricsBindAddress,
		"metrics and status server bind address",
	)
	flags.String(
		"health-probe-bind-address",
		defaults.Observability.HealthProbeBindAddress,
		"health probe server bind address",
	)
	flags.String(
		"log-level",
		string(defaults.Logging.Level),
		"log level: debug, info, warn, error",
	)
	flags.String("log-format", string(defaults.Logging.Format), "log format: json or console")

	bindings := map[string]string{
		"kubernetes.kubeconfig":                   "kubeconfig",
		"kubernetes.qps":                          "kubernetes-qps",
		"kubernetes.burst":                        "kubernetes-burst",
		"routes.interface":                        "interface",
		"routes.pod-cidr":                         "pod-cidr",
		"routes.service-cidr":                     "service-cidr",
		"routes.router-cidr":                      "router-cidr",
		"routes.table":                            "route-table",
		"routes.protocol":                         "route-protocol",
		"controller.reconcile-period":             "reconcile-period",
		"controller.dry-run":                      "dry-run",
		"probe.port":                              "probe-port",
		"probe.timeout":                           "probe-timeout",
		"probe.failure-threshold":                 "probe-failure-threshold",
		"probe.success-threshold":                 "probe-success-threshold",
		"observability.metrics-bind-address":      "metrics-bind-address",
		"observability.health-probe-bind-address": "health-probe-bind-address",
		"logging.level":                           "log-level",
		"logging.format":                          "log-format",
	}
	for key, flagName := range bindings {
		if err := values.BindPFlag(key, flags.Lookup(flagName)); err != nil {
			return fmt.Errorf("bind flag --%s: %w", flagName, err)
		}
	}

	return nil
}

func setDefaults(values *viper.Viper) {
	defaults := Defaults()
	values.SetDefault("kubernetes.kubeconfig", defaults.Kubernetes.Kubeconfig)
	values.SetDefault("kubernetes.qps", defaults.Kubernetes.QPS)
	values.SetDefault("kubernetes.burst", defaults.Kubernetes.Burst)
	values.SetDefault("routes.interface", defaults.Routes.Interface)
	values.SetDefault("routes.table", defaults.Routes.Table)
	values.SetDefault("routes.protocol", defaults.Routes.Protocol)
	values.SetDefault("controller.reconcile-period", defaults.Controller.ReconcilePeriod)
	values.SetDefault("controller.dry-run", defaults.Controller.DryRun)
	values.SetDefault("probe.port", defaults.Probe.Port)
	values.SetDefault("probe.timeout", defaults.Probe.Timeout)
	values.SetDefault("probe.failure-threshold", defaults.Probe.FailureThreshold)
	values.SetDefault("probe.success-threshold", defaults.Probe.SuccessThreshold)
	values.SetDefault(
		"observability.metrics-bind-address",
		defaults.Observability.MetricsBindAddress,
	)
	values.SetDefault(
		"observability.health-probe-bind-address",
		defaults.Observability.HealthProbeBindAddress,
	)
	values.SetDefault("logging.level", defaults.Logging.Level)
	values.SetDefault("logging.format", defaults.Logging.Format)
}
