package config

type Config struct {
	Gateway   Gateway `mapstructure:"gateway"`
	MCPServer Servers `mapstructure:"mcpServers"`

	Redis  RedisConfig  `mapstructure:"redis"`
	SQLite SQLiteConfig `mapstructure:"sqlite"`
}

type Gateway struct {
	Port int `mapstructure:"port"`

	Key  string `mapstructure:"key"`
	Cert string `mapstructure:"cert"`
}
