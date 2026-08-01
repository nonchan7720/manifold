package config

// MemoryConfig はインメモリストレージの設定。
type MemoryConfig struct {
	// Enabled が true の場合、インメモリストレージを使用する。
	Enabled bool `mapstructure:"enabled"`
}
