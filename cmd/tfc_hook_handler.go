package cmd

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/zapier/tfbuddy/pkg/hooks"
)

var gitlabToken string
var otelEnabled bool
var otelCollectorHost string
var otelCollectorPort string

// tfcHookHandlerCmd represents the run command
var tfcHookHandlerCmd = &cobra.Command{
	Use:   "handler",
	Short: "Start a hooks handler for Gitlab & Terraform cloud.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		t, err := initTelemetry(ctx)
		if err != nil {
			panic(err)
		}
		defer t.Shutdown()

		hooks.StartServer()

	},
	PreRunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

func init() {
	tfcCmd.AddCommand(tfcHookHandlerCmd)
	tfcHookHandlerCmd.PersistentFlags().StringVar(&gitlabToken, "gitlab_token", "", "Gitlab API token. (GITLAB_TOKEN)")
}
