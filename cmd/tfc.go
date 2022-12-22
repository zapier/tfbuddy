package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var tfcToken string

// tfcCmd represents the tfc command
var tfcCmd = &cobra.Command{
	Use:   "tfc",
	Short: "Sub commands for Terraform Cloud",
	Long:  ``,

	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if tfcToken == "" {
			tokenEnvNames := []string{"TFC_TOKEN", "TFE_TOKEN"}
			for _, env := range tokenEnvNames {
				tfcToken = os.Getenv(env)
				if tfcToken != "" {
					return nil
				}
			}
			return fmt.Errorf("env TFC_TOKEN is not set")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(tfcCmd)

	tfcCmd.PersistentFlags().StringVar(&tfcToken, "tfc_token", "", "Terraform Enterprise / Terraform Cloud API token. (TFC_TOKEN)")
}
