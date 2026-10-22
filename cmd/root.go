package cmd

import (
	"context"
	"os"
	"time"

	defaultlog "log"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var rootCmd = &cobra.Command{
	Use:   "datadog-prometheus-exporter",
	Short: "Exposes Datadog metrics as scrapable Prometheus metrics",
}

func Execute(ctx context.Context) {
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	configureLogging()
}

func configureLogging() {
	loggerConfig := zap.NewProductionConfig()
	loggerConfig.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.RFC3339)

	logger, err := loggerConfig.Build()
	zap.ReplaceGlobals(logger)

	if err != nil {
		defaultlog.Fatal(err)
	}
}
