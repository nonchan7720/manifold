package cmd

import (
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile      string
	globalConfig *config.Config
)

var rootCmd = &cobra.Command{
	Use:           "manifold",
	Short:         "manifold — mcp gateway service",
	Long:          `manifold - mcp gateway service A component that combines multiple inputs into a single output`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func initialize() {
	cfg, err := config.Load(rootCmd.Context(), cfgFile)
	if err != nil {
		panic(err)
	}
	globalConfig = cfg
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initialize)
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file")
	rootCmd.AddCommand(
		newGatewayCmd(),
	)
}
