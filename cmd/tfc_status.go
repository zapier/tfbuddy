package cmd

import (
	"github.com/spf13/cobra"
	"github.com/zapier/tfbuddy/pkg/tfc_utils"
)

// tfcStatusCmd represents the status command
var tfcStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Monitor the status of TFC runs for a merge request.",
	Long:  ``,
	Run: func(cmd *cobra.Command, args []string) {
		tfc_utils.MonitorRunStatus()
	},
}

func init() {
	tfcCmd.AddCommand(tfcStatusCmd)

}
