package config

// SQLiteConfig はSQLiteストレージの設定。
type SQLiteConfig struct {
	// Path はSQLiteデータベースファイルのパス。
	// ":memory:" を指定するとインメモリDBとして動作する。
	Path string `mapstructure:"path"`
}
