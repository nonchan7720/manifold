package telemetry

type Config struct {
	Enabled         bool   `mapstructure:"enabled"`
	AgentAddr       string `mapstructure:"agentAddr"`
	ServiceName     string `mapstructure:"serviceName"`
	Environment     string `mapstructure:"environment"`
	GzipCompression bool   `mapstructure:"gzipCompression"`

	HTTP *HTTP `mapstructure:"http"`
	GRPC *GRPC `mapstructure:"grpc"`
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
