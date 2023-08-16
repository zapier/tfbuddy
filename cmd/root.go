package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/zapier/tfbuddy/internal/logging"
	"github.com/zapier/tfbuddy/internal/telemetry"
	"github.com/zapier/tfbuddy/pkg"

	"github.com/spf13/viper"
)

var cfgFile string
var logLevel string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "tfbuddy",
	Short: "Various utilties to aid Terraform CI pipelines & Terraform Cloud runs",
	Long:  ``,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		logging.SetupLogOutput(resolveLogLevel())
	},
}

func resolveLogLevel() zerolog.Level {
	logLevelEnv := os.Getenv("TFBUDDY_LOG_LEVEL")
	if logLevelEnv != "" {
		logLevel = logLevelEnv
	}

	lvl, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		fmt.Println("could not parse log level, defaulting to 'info'")
		lvl = zerolog.InfoLevel
	}
	return lvl
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "v", "info", "Set the log output level (info, debug, trace)")
}

func initTelemetry(ctx context.Context) (*telemetry.OperatorTelemetry, error) {
	enableOtel, _ := strconv.ParseBool(os.Getenv("TFBUDDY_OTEL_ENABLED"))
	otelHost := os.Getenv("TFBUDDY_OTEL_COLLECTOR_HOST")
	otelPort := os.Getenv("TFBUDDY_OTEL_COLLECTOR_PORT")

	opts := telemetry.Options{
		Enabled:   enableOtel,
		Host:      otelHost,
		Port:      otelPort,
		Version:   pkg.GitTag,
		CommitSHA: pkg.GitCommit,
	}
	fmt.Printf("enabled: %v\thost: %s\tport: %s\n", enableOtel, otelHost, otelPort)

	fmt.Printf("OpenTelemetry Opts: %+v\n", opts)

	return telemetry.Init(ctx, "tfbuddy", opts)
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".tfbuddy" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".tfbuddy")
	}

	viper.SetEnvPrefix("TFBUDDY")
	viper.EnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}
