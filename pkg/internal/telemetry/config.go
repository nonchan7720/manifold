package telemetry

type ExporterType string

const (
	ExporterTypePull ExporterType = "pull"
	ExporterTypePush ExporterType = "push"
)

type Config struct {
	ServiceName     string `mapstructure:"serviceName"`
	Environment     string `mapstructure:"environment"`
	GzipCompression bool   `mapstructure:"gzipCompression"`

	Trace   Trace         `mapstructure:"trace"`
	Metrics MetricsConfig `mapstructure:"metrics"`
	Logs    LogsConfig    `mapstructure:"logs"`
}

type Trace struct {
	Enabled bool  `mapstructure:"enabled"`
	HTTP    *HTTP `mapstructure:"http"`
	GRPC    *GRPC `mapstructure:"grpc"`
}

type MetricsConfig struct {
	Enabled      bool         `mapstructure:"enabled"`
	ExporterType ExporterType `mapstructure:"exporterType"`

	// push 用（ExporterType == "push" の時のみ使用）
	HTTP *HTTP `mapstructure:"http"`
	GRPC *GRPC `mapstructure:"grpc"`
}

type LogsConfig struct {
	Enabled bool  `mapstructure:"enabled"`
	HTTP    *HTTP `mapstructure:"http"`
	GRPC    *GRPC `mapstructure:"grpc"`
}

type Endpoint struct {
	Endpoint    string `mapstructure:"addr"`
	EndpointURL string `mapstructure:"url"`
}

type HTTP struct {
	Endpoint `mapstructure:",squash"`
}

type GRPC struct {
	Endpoint `mapstructure:",squash"`
	Insecure bool `mapstructure:"insecure"`
}
