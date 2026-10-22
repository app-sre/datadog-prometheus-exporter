package cmd

import (
	"datadog-prometheus-exporter/serve"

	"github.com/spf13/cobra"
)

var (
	// cli flags
	listenPort        int
	observabilityPort int
	configFile        string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Poll Datadog and serve scrape requests",
	Run: func(cmd *cobra.Command, args []string) {
		serve.CollectAndServe(configFile, listenPort, observabilityPort, cmd.Context())
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().IntVarP(&listenPort, "port", "l", 9091, "listen port for incoming scrape requests")
	serveCmd.Flags().StringVarP(&configFile, "config", "c", "/config/config.yml", "flow config file")
	serveCmd.Flags().IntVarP(&observabilityPort, "observability-port", "p", 9090, "port for exporter self observability")
}
