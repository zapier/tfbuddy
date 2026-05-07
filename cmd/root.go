package cmd

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/zapier/tfbuddy/internal/config"
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
	lvl, err := zerolog.ParseLevel(config.C.LogLevel)
	if err != nil {
		log.Println("could not parse log level, defaulting to 'info'")
		lvl = zerolog.InfoLevel
	}
	return lvl
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	logging.SetupLogOutput(resolveLogLevel())
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	config.Init()
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "v", "info", "Set the log output level (info, debug, trace)")
	cobra.CheckErr(viper.BindPFlag(config.KeyLogLevel, rootCmd.PersistentFlags().Lookup("log-level")))
}

func initTelemetry(ctx context.Context) (*telemetry.OperatorTelemetry, error) {
	return telemetry.Init(ctx, "tfbuddy", telemetry.Options{
		Enabled:   config.C.OTELEnabled,
		Host:      config.C.OTELCollectorHost,
		Port:      config.C.OTELCollectorPort,
		Version:   pkg.GitTag,
		CommitSHA: pkg.GitCommit,
	})
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

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
	config.Reload()
}
