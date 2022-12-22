package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zapier/tfbuddy/pkg/tfc_utils"
)

var tfcWorkspace string

// tfcRunCmd represents the run command
var tfcRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Start a run for a Terraform workspace.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		tfc_utils.StartScheduledRun(tfcToken, tfcWorkspace)
	},
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if tfcWorkspace == "" {
			tfcWorkspace = os.Getenv("TFC_WORKSPACE_NAME")
			if tfcWorkspace == "" {
				return fmt.Errorf("TFC_WORKSPACE_NAME is not set")
			}
		}
		return nil
	},
}

func init() {
	tfcCmd.AddCommand(tfcRunCmd)

	tfcRunCmd.Flags().StringVar(&tfcWorkspace, "tfc_workspace", "", "The Terraform Cloud workspace name. (TFC_WORKSPACE_NAME)")
}
