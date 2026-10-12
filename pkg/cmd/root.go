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
	// サブコマンドの実行前に設定ファイルを読み込む。設定ファイルを使わない
	// サブコマンド（guardrail のサイドカー等）は PersistentPreRunE を上書きする。
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := config.Load(cmd.Context(), cfgFile)
		if err != nil {
			return err
		}
		globalConfig = cfg
		return nil
	},
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file")
	rootCmd.AddCommand(
		newGatewayCmd(),
		newOpenAPICmd(),
		newGuardrailCmd(),
	)
}
