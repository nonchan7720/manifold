package cmd

import (
	"fmt"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/spf13/cobra"
)

var globalConfig *config.Config

var rootCmd = &cobra.Command{
	Use:   "manifold",
	Short: "manifold — mcp gateway service",
	Long:  `manifold - mcp gateway service A component that combines multiple inputs into a single output`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("failed resolving config: %w", err)
		}
		globalConfig = cfg
		return nil
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(
		newGatewayCmd(),
	)
}
