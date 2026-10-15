package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/nonchan7720/manifold/pkg/services/guardrail"
	"github.com/spf13/cobra"
)

func newGuardrailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrail",
		Short: "Guardrail adapters serving the Generic Guardrail API",
		// サイドカーとして manifold の設定ファイル無しで起動するため、root の
		// 設定ファイルの読み込みを行わない。
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	cmd.AddCommand(newGuardrailBedrockCmd())
	return cmd
}

// bedrockGuardrailOptions は manifold guardrail bedrock の設定。サイドカーとして
// 起動するため、manifold の設定ファイルではなくフラグ（と環境変数）で受け取る。
type bedrockGuardrailOptions struct {
	port    int
	id      string
	version string
	region  string
}

func newGuardrailBedrockCmd() *cobra.Command {
	opts := bedrockGuardrailOptions{
		port:    8090,
		id:      os.Getenv("BEDROCK_GUARDRAIL_ID"),
		version: os.Getenv("BEDROCK_GUARDRAIL_VERSION"),
	}
	cmd := &cobra.Command{
		Use:   "bedrock",
		Short: "Serve the Generic Guardrail API backed by AWS Bedrock Guardrails",
		Long: "Serves the Generic Guardrail API (POST " + guardrail.APIPath + ") and " +
			"translates each request to AWS Bedrock Guardrails' ApplyGuardrail. Run it as a " +
			"sidecar and point guardrail.url at it. AWS credentials and the region are " +
			"resolved the standard AWS SDK way (environment, shared config, IRSA, ...).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBedrockGuardrail(cmd.Context(), opts)
		},
	}
	cmd.Flags().IntVar(&opts.port, "port", opts.port, "listening port")
	cmd.Flags().StringVar(&opts.id, "guardrail-id", opts.id,
		"Bedrock guardrail identifier (env BEDROCK_GUARDRAIL_ID)")
	cmd.Flags().StringVar(&opts.version, "guardrail-version", opts.version,
		"Bedrock guardrail version, e.g. 1 or DRAFT (env BEDROCK_GUARDRAIL_VERSION)")
	cmd.Flags().StringVar(&opts.region, "region", "",
		"AWS region (default: resolved by the AWS SDK, e.g. AWS_REGION)")
	return cmd
}

func runBedrockGuardrail(ctx context.Context, opts bedrockGuardrailOptions) error {
	if opts.id == "" || opts.version == "" {
		return fmt.Errorf("--guardrail-id and --guardrail-version are required")
	}
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	var loadOpts []func(*awsconfig.LoadOptions) error
	if opts.region != "" {
		loadOpts = append(loadOpts, awsconfig.WithRegion(opts.region))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("POST "+guardrail.APIPath, guardrail.NewBedrockHandler(
		bedrockruntime.NewFromConfig(awsCfg), opts.id, opts.version,
	))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// 1リクエストは最大 16MiB の本文を読み、texts を ApplyGuardrail してから応答する。
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", opts.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return runServer(ctx, srv, "guardrail-bedrock", opts.port, "", "", shutdownOptions{})
}
