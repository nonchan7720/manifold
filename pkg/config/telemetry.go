package config

type Telemetry struct {
	Enabled     bool   `yaml:"enabled"`
	AgentAddr   string `yaml:"agentAddr"`
	ServiceName string `yaml:"serviceName"`
	Environment string `yaml:"environment"`
	EnabledHTTP bool   `yaml:"http"`
}
