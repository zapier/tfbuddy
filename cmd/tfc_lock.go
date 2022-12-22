package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zapier/tfbuddy/pkg/tfc_utils"
)

// tfcLockCmd represents the lock/unlock command
var tfcLockCmd = &cobra.Command{
	Use:   "lock",
	Short: "Lock a Terraform workspace.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		tfc_utils.LockUnlockWorkspace(tfcToken, tfcWorkspace, true, "Locked from MR")
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
	tfcCmd.AddCommand(tfcLockCmd)

	tfcLockCmd.Flags().StringVar(&tfcWorkspace, "tfc_workspace", "", "The Terraform Cloud workspace name. (TFC_WORKSPACE_NAME)")
}
