package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/zapier/tfbuddy/pkg/tfc_utils"
)

// tfcLockCmd represents the lock/unlock command
var tfcUnlockCmd = &cobra.Command{
	Use:   "unlock",
	Short: "Unlock a Terraform workspace.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		tfc_utils.LockUnlockWorkspace(ctx, tfcToken, tfcWorkspace, false, "")
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
	tfcCmd.AddCommand(tfcUnlockCmd)

	tfcUnlockCmd.Flags().StringVar(&tfcWorkspace, "tfc_workspace", "", "The Terraform Cloud workspace name. (TFC_WORKSPACE_NAME)")
}
