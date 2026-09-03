// Package config loads and validates route-controller configuration.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"time"
)

const (
	DefaultKubeconfig             = "/etc/route-controller/kubeconfig"
	DefaultInterface              = "eth0"
	DefaultRouteTable             = 254
	DefaultRouteProtocol          = 99
	DefaultReconcilePeriod        = 10 * time.Second
	DefaultProbePort              = 4240
	DefaultProbeTimeout           = 2 * time.Second
	DefaultProbeFailureThreshold  = 3
	DefaultProbeSuccessThreshold  = 1
	DefaultMetricsBindAddress     = "127.0.0.1:9918"
	DefaultHealthProbeBindAddress = "127.0.0.1:9919"
	DefaultKubernetesQPS          = 10
	DefaultKubernetesBurst        = 20
	minimumLinuxRouteTable        = 1
	maximumLinuxRouteTable        = 2_147_483_647
	minimumLinuxRouteProtocol     = 1
	maximumLinuxRouteProtocol     = 255
	maximumNetworkPort            = 65_535
)

type LogFormat string

const (
	LogFormatJSON    LogFormat = "json"
	LogFormatConsole LogFormat = "console"
)

type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

type Config struct {
	Kubernetes    KubernetesConfig    `json:"kubernetes"    mapstructure:"kubernetes"    yaml:"kubernetes"`
	Routes        RoutesConfig        `json:"routes"        mapstructure:"routes"        yaml:"routes"`
	Controller    ControllerConfig    `json:"controller"    mapstructure:"controller"    yaml:"controller"`
	Probe         ProbeConfig         `json:"probe"         mapstructure:"probe"         yaml:"probe"`
	Observability ObservabilityConfig `json:"observability" mapstructure:"observability" yaml:"observability"`
	Logging       LoggingConfig       `json:"logging"       mapstructure:"logging"       yaml:"logging"`
}

type KubernetesConfig struct {
	Kubeconfig string  `json:"kubeconfig" mapstructure:"kubeconfig" yaml:"kubeconfig"`
	QPS        float32 `json:"qps"        mapstructure:"qps"        yaml:"qps"`
	Burst      int     `json:"burst"      mapstructure:"burst"      yaml:"burst"`
}

type RoutesConfig struct {
	Interface   string `json:"interface"    mapstructure:"interface"    yaml:"interface"`
	PodCIDR     string `json:"pod-cidr"     mapstructure:"pod-cidr"     yaml:"pod-cidr"`
	ServiceCIDR string `json:"service-cidr" mapstructure:"service-cidr" yaml:"service-cidr"`
	RouterCIDR  string `json:"router-cidr"  mapstructure:"router-cidr"  yaml:"router-cidr"`
	Table       int    `json:"table"        mapstructure:"table"        yaml:"table"`
	Protocol    int    `json:"protocol"     mapstructure:"protocol"     yaml:"protocol"`
}

type ControllerConfig struct {
	ReconcilePeriod time.Duration `json:"reconcile-period" mapstructure:"reconcile-period" yaml:"reconcile-period"`
	DryRun          bool          `json:"dry-run"          mapstructure:"dry-run"          yaml:"dry-run"`
}

type ProbeConfig struct {
	Port             int           `json:"port"              mapstructure:"port"              yaml:"port"`
	Timeout          time.Duration `json:"timeout"           mapstructure:"timeout"           yaml:"timeout"`
	FailureThreshold int           `json:"failure-threshold" mapstructure:"failure-threshold" yaml:"failure-threshold"`
	SuccessThreshold int           `json:"success-threshold" mapstructure:"success-threshold" yaml:"success-threshold"`
}

type ObservabilityConfig struct {
	MetricsBindAddress     string `json:"metrics-bind-address"      mapstructure:"metrics-bind-address"      yaml:"metrics-bind-address"`
	HealthProbeBindAddress string `json:"health-probe-bind-address" mapstructure:"health-probe-bind-address" yaml:"health-probe-bind-address"`
}

type LoggingConfig struct {
	Level  LogLevel  `json:"level"  mapstructure:"level"  yaml:"level"`
	Format LogFormat `json:"format" mapstructure:"format" yaml:"format"`
}

type Validated struct {
	Config
	PodCIDR     netip.Prefix
	ServiceCIDR netip.Prefix
	RouterCIDR  netip.Prefix
}

func Defaults() Config {
	return Config{
		Kubernetes: KubernetesConfig{
			Kubeconfig: DefaultKubeconfig,
			QPS:        DefaultKubernetesQPS,
			Burst:      DefaultKubernetesBurst,
		},
		Routes: RoutesConfig{
			Interface: DefaultInterface,
			Table:     DefaultRouteTable,
			Protocol:  DefaultRouteProtocol,
		},
		Controller: ControllerConfig{ReconcilePeriod: DefaultReconcilePeriod},
		Probe: ProbeConfig{
			Port:             DefaultProbePort,
			Timeout:          DefaultProbeTimeout,
			FailureThreshold: DefaultProbeFailureThreshold,
			SuccessThreshold: DefaultProbeSuccessThreshold,
		},
		Observability: ObservabilityConfig{
			MetricsBindAddress:     DefaultMetricsBindAddress,
			HealthProbeBindAddress: DefaultHealthProbeBindAddress,
		},
		Logging: LoggingConfig{Level: LogLevelInfo, Format: LogFormatJSON},
	}
}

func (cfg Config) Validate() (Validated, error) {
	validationErrors := cfg.validateScalars()

	podCIDR, err := parseIPv4Prefix("routes.pod-cidr", cfg.Routes.PodCIDR)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	serviceCIDR, err := parseIPv4Prefix("routes.service-cidr", cfg.Routes.ServiceCIDR)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	routerCIDR, err := parseIPv4Prefix("routes.router-cidr", cfg.Routes.RouterCIDR)
	if err != nil {
		validationErrors = append(validationErrors, err)
	}

	validationErrors = append(
		validationErrors,
		validatePrefixRelationships(podCIDR, serviceCIDR, routerCIDR)...,
	)

	if err := errors.Join(validationErrors...); err != nil {
		return Validated{}, fmt.Errorf("invalid configuration: %w", err)
	}

	return Validated{
		Config:      cfg,
		PodCIDR:     podCIDR,
		ServiceCIDR: serviceCIDR,
		RouterCIDR:  routerCIDR,
	}, nil
}

func (cfg Config) validateScalars() []error {
	validationErrors := make([]error, 0)
	if cfg.Kubernetes.Kubeconfig == "" {
		validationErrors = append(validationErrors, errors.New("kubernetes.kubeconfig is required"))
	}

	if cfg.Kubernetes.QPS <= 0 || cfg.Kubernetes.Burst <= 0 {
		validationErrors = append(
			validationErrors,
			errors.New("kubernetes.qps and kubernetes.burst must be positive"),
		)
	}

	if cfg.Routes.Interface == "" {
		validationErrors = append(validationErrors, errors.New("routes.interface is required"))
	}

	if cfg.Routes.Table < minimumLinuxRouteTable || cfg.Routes.Table > maximumLinuxRouteTable {
		validationErrors = append(
			validationErrors,
			fmt.Errorf(
				"routes.table must be between %d and %d",
				minimumLinuxRouteTable,
				maximumLinuxRouteTable,
			),
		)
	}

	if cfg.Routes.Protocol < minimumLinuxRouteProtocol ||
		cfg.Routes.Protocol > maximumLinuxRouteProtocol {
		validationErrors = append(
			validationErrors,
			fmt.Errorf(
				"routes.protocol must be between %d and %d",
				minimumLinuxRouteProtocol,
				maximumLinuxRouteProtocol,
			),
		)
	}

	if cfg.Controller.ReconcilePeriod <= 0 {
		validationErrors = append(
			validationErrors,
			errors.New("controller.reconcile-period must be positive"),
		)
	}

	if cfg.Probe.Port < 1 || cfg.Probe.Port > maximumNetworkPort {
		validationErrors = append(
			validationErrors,
			fmt.Errorf("probe.port must be between 1 and %d", maximumNetworkPort),
		)
	}

	if cfg.Probe.Timeout <= 0 || cfg.Probe.FailureThreshold <= 0 ||
		cfg.Probe.SuccessThreshold <= 0 {
		validationErrors = append(
			validationErrors,
			errors.New("probe.timeout and probe thresholds must be positive"),
		)
	}

	if cfg.Observability.MetricsBindAddress == "" ||
		cfg.Observability.HealthProbeBindAddress == "" {
		validationErrors = append(
			validationErrors,
			errors.New("observability bind addresses are required"),
		)
	}

	if cfg.Logging.Format != LogFormatJSON && cfg.Logging.Format != LogFormatConsole {
		validationErrors = append(
			validationErrors,
			fmt.Errorf("logging.format must be %q or %q", LogFormatJSON, LogFormatConsole),
		)
	}

	switch cfg.Logging.Level {
	case LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError:
	default:
		validationErrors = append(
			validationErrors,
			fmt.Errorf(
				"logging.level must be %q, %q, %q, or %q",
				LogLevelDebug,
				LogLevelInfo,
				LogLevelWarn,
				LogLevelError,
			),
		)
	}

	return validationErrors
}

func validatePrefixRelationships(podCIDR, serviceCIDR, routerCIDR netip.Prefix) []error {
	validationErrors := make([]error, 0, 3)
	if podCIDR.IsValid() && serviceCIDR.IsValid() && prefixesOverlap(podCIDR, serviceCIDR) {
		validationErrors = append(
			validationErrors,
			errors.New("routes.pod-cidr and routes.service-cidr overlap"),
		)
	}

	if podCIDR.IsValid() && routerCIDR.IsValid() && prefixesOverlap(podCIDR, routerCIDR) {
		validationErrors = append(
			validationErrors,
			errors.New("routes.pod-cidr and routes.router-cidr overlap"),
		)
	}

	if serviceCIDR.IsValid() && routerCIDR.IsValid() && prefixesOverlap(serviceCIDR, routerCIDR) {
		validationErrors = append(
			validationErrors,
			errors.New("routes.service-cidr and routes.router-cidr overlap"),
		)
	}

	return validationErrors
}

func parseIPv4Prefix(name, value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("%s must be a valid IPv4 CIDR", name)
	}

	return prefix.Masked(), nil
}

func prefixesOverlap(first, second netip.Prefix) bool {
	return first.Contains(second.Addr()) || second.Contains(first.Addr())
}
