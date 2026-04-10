package config

type RedisConfig struct {
	URL         string   `mapstructure:"url"`
	Addrs       []string `mapstructure:"addrs"`
	User        string   `mapstructure:"user"`
	Password    string   `mapstructure:"password"`
	DB          int      `mapstructure:"db"`
	MasterName  string   `mapstructure:"master_name"`
	TLS         bool     `mapstructure:"tls"`
	ClusterMode bool     `mapstructure:"cluster_mode"`
}
