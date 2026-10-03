package cmd

import (
	"context"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/spf13/cobra"
)

var (
	cfgFile      string
	globalConfig *config.Config
)

// quickStart holds the --openapi family of flags shared by the gateway and
// stdio commands (only one command runs per process).
var quickStart quickStartFlags

var rootCmd = &cobra.Command{
	Use:               "manifold",
	Short:             "manifold — mcp gateway service",
	Long:              `manifold - mcp gateway service A component that combines multiple inputs into a single output`,
	SilenceErrors:     true,
	SilenceUsage:      true,
	PersistentPreRunE: loadGlobalConfig,
}

// loadGlobalConfig loads globalConfig before any subcommand runs: from the
// --openapi quick start flags when given, otherwise from the config file.
// help and completion need no config.
func loadGlobalConfig(cmd *cobra.Command, _ []string) error {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Name() == "help" || c.Name() == "completion" {
			return nil
		}
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	var (
		cfg *config.Config
		err error
	)
	if quickStart.enabled() {
		cfg, err = quickStart.config(ctx)
	} else {
		cfg, err = config.Load(ctx, cfgFile)
	}
	if err != nil {
		return err
	}
	globalConfig = cfg
	return nil
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file")
	rootCmd.AddCommand(
		newGatewayCmd(),
		newStdioCmd(),
		newOpenAPICmd(),
	)
}
