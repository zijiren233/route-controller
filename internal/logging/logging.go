// Package logging configures the controller-runtime Zap logger.
package logging

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/zijiren233/route-controller/internal/config"
	"go.uber.org/zap/zapcore"
	controllerZap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func New(settings config.LoggingConfig) (logr.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(string(settings.Level))); err != nil {
		return logr.Logger{}, fmt.Errorf("parse logging.level %q: %w", settings.Level, err)
	}

	options := []controllerZap.Opts{
		controllerZap.Level(level),
		controllerZap.UseDevMode(false),
	}
	switch settings.Format {
	case config.LogFormatJSON:
		options = append(options, controllerZap.JSONEncoder())
	case config.LogFormatConsole:
		options = append(options, controllerZap.ConsoleEncoder())
	default:
		return logr.Logger{}, fmt.Errorf("unsupported log format %q", settings.Format)
	}

	return controllerZap.New(options...), nil
}
