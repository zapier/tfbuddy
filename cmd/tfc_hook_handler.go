package cmd

import (
	"github.com/spf13/cobra"

	"github.com/zapier/tfbuddy/internal/logging"
	"github.com/zapier/tfbuddy/pkg/hooks"
)

var gitlabToken string

// tfcHookHandlerCmd represents the run command
var tfcHookHandlerCmd = &cobra.Command{
	Use:   "handler",
	Short: "Start a hooks handler for Gitlab & Terraform cloud.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		logging.SetupLogOutput(resolveLogLevel())
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
