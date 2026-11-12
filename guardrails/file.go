package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"github.com/nonchan7720/manifold/guardrails/internal/extract/azure"
	"github.com/nonchan7720/manifold/guardrails/internal/extract/bedrock"
	"github.com/nonchan7720/manifold/guardrails/internal/extract/google"
	"github.com/nonchan7720/manifold/guardrails/internal/extract/openapi"
	"github.com/nonchan7720/manifold/guardrails/internal/fileguard"
	"google.golang.org/genai"
)

// Extractor の種類（--extractor）。
const (
	extractorNone    = "none"
	extractorBedrock = "bedrock"
	extractorGoogle  = "google"
	extractorAzure   = "azure"
	extractorOpenAPI = "openapi"
)

// stringList は値を繰り返し（またはカンマ区切りで）受け取るフラグ。
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(s string) error {
	for value := range strings.SplitSeq(s, ",") {
		if value = strings.TrimSpace(value); value != "" {
			*l = append(*l, value)
		}
	}
	return nil
}

// keyValues は "name=value" を繰り返し受け取るフラグ。
type keyValues map[string]string

func (kv keyValues) String() string { return fmt.Sprintf("%d entries", len(kv)) }

func (kv keyValues) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return fmt.Errorf("must be name=value")
	}
	kv[name] = value
	return nil
}

// fileOptions は manifold-guardrail file の設定。
type fileOptions struct {
	host              string
	port              int
	downstreamURL     string
	downstreamHeaders keyValues

	extractor          string
	model              string
	maxOutputTokens    int
	extractTimeout     time.Duration
	concurrency        int
	maxFileBytes       int
	allowUnsupported   bool
	allowIntervened    bool
	extractedInputType string
	chunkChars         int
	chunkOverlapChars  int
	maxChunks          int

	fetchAllowedHosts stringList
	fetchMaxBytes     int64
	fetchTimeout      time.Duration

	awsRegion string

	googleBackend  string
	googleProject  string
	googleLocation string

	azureEndpoint string

	openapiSpec      string
	openapiOperation string
	openapiServer    string
	openapiHeaders   keyValues
	openapiParams    keyValues
	openapiFileField string
	openapiTextPath  string
	openapiMIMETypes string
}

func parseFileFlags(args []string) (fileOptions, error) {
	opts := fileOptions{
		downstreamHeaders: keyValues{},
		openapiHeaders:    keyValues{},
		openapiParams:     keyValues{},
	}
	fs := flag.NewFlagSet("manifold-guardrail file", flag.ContinueOnError)
	fs.StringVar(&opts.host, "host", "",
		"listening address; empty listens on every interface. The endpoint has no "+
			"authentication, so set 127.0.0.1 when only a caller in the same Pod or host calls it")
	fs.IntVar(&opts.port, "port", 8091, "listening port")
	fs.StringVar(&opts.downstreamURL, "downstream-url", os.Getenv("FILE_GUARDRAIL_DOWNSTREAM_URL"),
		"Generic Guardrail API endpoint of the text guardrail that checks the transcribed text "+
			"(env FILE_GUARDRAIL_DOWNSTREAM_URL)")
	fs.Var(opts.downstreamHeaders, "downstream-header",
		"name=value header sent to the downstream guardrail (repeatable)")

	fs.StringVar(&opts.extractor, "extractor", extractorNone,
		"how files that are not plain text are transcribed: bedrock, google, azure, openapi, "+
			"or none (only text files are checked)")
	fs.StringVar(&opts.model, "model", os.Getenv("FILE_GUARDRAIL_MODEL"),
		"model used to transcribe: Bedrock model ID / inference profile, Gemini model name, "+
			"or Azure OpenAI deployment name (env FILE_GUARDRAIL_MODEL)")
	fs.IntVar(&opts.maxOutputTokens, "max-output-tokens", 8192,
		"output token limit of one transcription; a file whose transcription is cut off is blocked")
	fs.DurationVar(&opts.extractTimeout, "extract-timeout", 2*time.Minute,
		"timeout of one transcription")
	fs.IntVar(&opts.concurrency, "concurrency", 4, "files of one request transcribed in parallel")
	fs.IntVar(&opts.maxFileBytes, "max-file-bytes", 0,
		"size limit of one file; larger files are blocked (0: no limit)")
	fs.BoolVar(&opts.allowUnsupported, "allow-unsupported-files", false,
		"pass files of a type that cannot be transcribed unchecked instead of blocking them")
	fs.BoolVar(&opts.allowIntervened, "allow-intervened", false,
		"pass a file when the downstream guardrail would rewrite its text (e.g. mask PII) "+
			"instead of blocking it; a file itself cannot be rewritten")
	fs.StringVar(&opts.extractedInputType, "extracted-input-type", "request",
		"input_type the transcribed text is sent downstream with: request, response, "+
			"or inherit (the input_type of the incoming request)")
	fs.IntVar(&opts.chunkChars, "chunk-chars", 0,
		"split the transcribed text into pieces of this many characters for the downstream "+
			"guardrail (0: do not split)")
	fs.IntVar(&opts.chunkOverlapChars, "chunk-overlap-chars", 200,
		"with --chunk-chars, how many characters neighbouring pieces share, so that a phrase "+
			"cut at a boundary is still whole in one piece: set it to the longest phrase the "+
			"downstream guardrail has to catch; must be positive and at most half of --chunk-chars")
	fs.IntVar(&opts.maxChunks, "max-chunks", fileguard.DefaultMaxChunks,
		"limit on the pieces of transcribed text one request sends downstream; a request "+
			"over it is blocked")

	fs.Var(&opts.fetchAllowedHosts, "fetch-allowed-host",
		"host whose http(s) URLs may be fetched to check a file given by URL, e.g. "+
			"files.example.com or minio.internal:9000 (exact host names; repeatable or "+
			"comma-separated); URLs of other hosts are never fetched")
	fs.Int64Var(&opts.fetchMaxBytes, "fetch-max-bytes", 20<<20,
		"size limit of a file fetched from a URL; larger files are blocked")
	fs.DurationVar(&opts.fetchTimeout, "fetch-timeout", 30*time.Second,
		"timeout of fetching one file from a URL")

	fs.StringVar(&opts.awsRegion, "aws-region", "",
		"bedrock: AWS region (default: resolved by the AWS SDK, e.g. AWS_REGION)")

	fs.StringVar(&opts.googleBackend, "google-backend", "gemini",
		"google: gemini (Gemini API, key in GEMINI_API_KEY or GOOGLE_API_KEY) or "+
			"vertex (Vertex AI, application default credentials)")
	fs.StringVar(&opts.googleProject, "google-project", os.Getenv("GOOGLE_CLOUD_PROJECT"),
		"google: project of Vertex AI (env GOOGLE_CLOUD_PROJECT)")
	fs.StringVar(&opts.googleLocation, "google-location", os.Getenv("GOOGLE_CLOUD_LOCATION"),
		"google: location of Vertex AI (env GOOGLE_CLOUD_LOCATION)")

	fs.StringVar(&opts.azureEndpoint, "azure-endpoint", os.Getenv("AZURE_OPENAI_ENDPOINT"),
		"azure: resource endpoint, e.g. https://<resource>.openai.azure.com; the key is read "+
			"from AZURE_OPENAI_API_KEY (env AZURE_OPENAI_ENDPOINT)")

	fs.StringVar(&opts.openapiSpec, "openapi-spec", "", "openapi: path of the OpenAPI spec")
	fs.StringVar(&opts.openapiOperation, "openapi-operation", "",
		"openapi: operationId of the operation that transcribes a file")
	fs.StringVar(&opts.openapiServer, "openapi-server", "",
		"openapi: base URL of the service (default: the first server of the spec)")
	fs.Var(opts.openapiHeaders, "openapi-header",
		"openapi: name=value header sent to the service, e.g. for authentication (repeatable)")
	fs.Var(opts.openapiParams, "openapi-param",
		"openapi: name=value for a parameter of the operation or a field of its request body; "+
			"{mime_type} in the value is replaced by the type of the file (repeatable)")
	fs.StringVar(&opts.openapiFileField, "openapi-file-field", "",
		"openapi: request body property the file goes in (default: decided from the spec)")
	fs.StringVar(&opts.openapiTextPath, "openapi-text-path", "",
		"openapi: where the text is in a JSON response, dot-separated with * for every "+
			"element, e.g. pages.*.text (default: decided from the spec)")
	fs.StringVar(&opts.openapiMIMETypes, "openapi-mime-types", "application/pdf,image/*",
		"openapi: comma-separated file types sent to the service")

	if err := fs.Parse(args); err != nil {
		return fileOptions{}, err
	}
	if fs.NArg() > 0 {
		return fileOptions{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, opts.validate()
}

func (o fileOptions) validate() error {
	if o.downstreamURL == "" {
		return errors.New("--downstream-url is required")
	}
	switch o.extractedInputType {
	case "request", "response", "inherit":
	default:
		return errors.New("--extracted-input-type must be request, response or inherit")
	}
	if o.maxOutputTokens <= 0 {
		return errors.New("--max-output-tokens must be positive")
	}
	if err := o.validateChunking(); err != nil {
		return err
	}
	if o.fetchMaxBytes <= 0 {
		return errors.New("--fetch-max-bytes must be positive")
	}
	switch o.extractor {
	case extractorNone:
	case extractorBedrock, extractorGoogle:
		if o.model == "" {
			return errors.New("--model is required")
		}
	case extractorAzure:
		if o.model == "" || o.azureEndpoint == "" {
			return errors.New("--model (the deployment name) and --azure-endpoint are required")
		}
		if os.Getenv("AZURE_OPENAI_API_KEY") == "" {
			return errors.New("AZURE_OPENAI_API_KEY is required")
		}
	case extractorOpenAPI:
		if o.openapiSpec == "" || o.openapiOperation == "" {
			return errors.New("--openapi-spec and --openapi-operation are required")
		}
	default:
		return fmt.Errorf("unknown --extractor %q", o.extractor)
	}
	return nil
}

// validateChunking は、起こしたテキストの分割（--chunk-chars など）の設定を検証する。
func (o fileOptions) validateChunking() error {
	if o.chunkChars < 0 || o.chunkOverlapChars < 0 {
		return errors.New("--chunk-chars and --chunk-overlap-chars must not be negative")
	}
	if o.chunkChars > 0 {
		// 重なりが無いと、境界で切れた語句は必ずどの断片にも全体が入らず、下流が
		// 検出できない。重なりが断片より小さくないと、分割が進まない。
		if o.chunkOverlapChars == 0 {
			return errors.New("--chunk-overlap-chars must be positive when --chunk-chars is set: " +
				"set it to the longest phrase the downstream guardrail has to catch")
		}
		// 重なりが断片の半分を超えると、断片が少しずつしか進まず、分割後の合計が
		// 元のテキストの何倍にも膨らむ（小さいファイルでメモリを使い切る）。
		if o.chunkOverlapChars > o.chunkChars/2 {
			return errors.New("--chunk-overlap-chars must be at most half of --chunk-chars")
		}
	}
	if o.maxChunks <= 0 {
		return errors.New("--max-chunks must be positive")
	}
	return nil
}

// newExtractor は opts の Extractor を作る。none なら nil（テキストのファイルだけ検査する）。
func newExtractor(
	ctx context.Context, opts fileOptions, httpClient *http.Client,
) (extract.Extractor, error) {
	maxTokens := int32(min(opts.maxOutputTokens, 1<<30)) //nolint:gosec // 上限を切っている
	switch opts.extractor {
	case extractorBedrock:
		var loadOpts []func(*awsconfig.LoadOptions) error
		if opts.awsRegion != "" {
			loadOpts = append(loadOpts, awsconfig.WithRegion(opts.awsRegion))
		}
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("load AWS config: %w", err)
		}
		return bedrock.New(bedrockruntime.NewFromConfig(awsCfg), opts.model, maxTokens), nil
	case extractorGoogle:
		cfg := &genai.ClientConfig{HTTPClient: httpClient}
		switch opts.googleBackend {
		case "gemini":
			cfg.Backend = genai.BackendGeminiAPI
			cfg.APIKey = os.Getenv("GEMINI_API_KEY")
			if cfg.APIKey == "" {
				cfg.APIKey = os.Getenv("GOOGLE_API_KEY")
			}
			if cfg.APIKey == "" {
				return nil, errors.New("GEMINI_API_KEY (or GOOGLE_API_KEY) is required")
			}
		case "vertex":
			cfg.Backend = genai.BackendVertexAI
			cfg.Project, cfg.Location = opts.googleProject, opts.googleLocation
			// Vertex AI はアプリケーションの既定の認証情報を使うので、HTTPClient は
			// SDK に作らせる（指定すると認証が付かない）。
			cfg.HTTPClient = nil
		default:
			return nil, errors.New("--google-backend must be gemini or vertex")
		}
		client, err := genai.NewClient(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("create Google GenAI client: %w", err)
		}
		return google.New(client.Models, opts.model, maxTokens), nil
	case extractorAzure:
		return azure.New(opts.azureEndpoint, os.Getenv("AZURE_OPENAI_API_KEY"),
			opts.model, opts.maxOutputTokens, httpClient), nil
	case extractorOpenAPI:
		return openapi.New(openapi.Config{
			SpecPath:    opts.openapiSpec,
			OperationID: opts.openapiOperation,
			ServerURL:   opts.openapiServer,
			Headers:     opts.openapiHeaders,
			Params:      opts.openapiParams,
			FileField:   opts.openapiFileField,
			TextPath:    opts.openapiTextPath,
			MIMETypes:   strings.Split(opts.openapiMIMETypes, ","),
		}, httpClient)
	}
	return nil, nil //nolint:nilnil // none は Extractor 無し
}

func runFile(ctx context.Context, args []string) error {
	opts, err := parseFileFlags(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	// 書き起こしと下流の呼び出しに使う。タイムアウトは1回の呼び出しの上限。
	httpClient := &http.Client{Timeout: opts.extractTimeout}
	extractor, err := newExtractor(ctx, opts, httpClient)
	if err != nil {
		return err
	}
	// 許可したホストが無ければ、URL のファイルは取得しない。
	var fetcher *fileguard.Fetcher
	if len(opts.fetchAllowedHosts) > 0 {
		fetcher, err = fileguard.NewFetcher(
			opts.fetchAllowedHosts, opts.fetchMaxBytes, opts.fetchTimeout,
		)
		if err != nil {
			return err
		}
	}
	inputType := opts.extractedInputType
	if inputType == "inherit" {
		inputType = ""
	}

	mux := http.NewServeMux()
	mux.Handle("POST "+fileguard.APIPath, fileguard.NewHandler(fileguard.Config{
		Extractor: extractor,
		Fetcher:   fetcher,
		Downstream: fileguard.NewDownstream(
			opts.downstreamURL,
			opts.downstreamHeaders,
			httpClient,
		),
		ExtractTimeout:     opts.extractTimeout,
		Concurrency:        opts.concurrency,
		MaxFileBytes:       opts.maxFileBytes,
		AllowUnsupported:   opts.allowUnsupported,
		AllowIntervened:    opts.allowIntervened,
		ExtractedInputType: inputType,
		ChunkChars:         opts.chunkChars,
		ChunkOverlap:       opts.chunkOverlapChars,
		MaxChunks:          opts.maxChunks,
	}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{
		Addr:              net.JoinHostPort(opts.host, strconv.Itoa(opts.port)),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		// 1リクエストで複数のファイルを起こすので、書き起こしのタイムアウトより長くする。
		WriteTimeout: 4*opts.extractTimeout + time.Minute,
		IdleTimeout:  2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.InfoContext(ctx, "file guardrail listening",
			slog.String("host", opts.host), slog.Int("port", opts.port),
			slog.String("extractor", opts.extractor))
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
