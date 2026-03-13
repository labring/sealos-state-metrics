package database

import "time"

// Config contains configuration for the Database collector
type Config struct {
	CheckInterval time.Duration `yaml:"checkInterval" json:"check_interval" env:"CHECK_INTERVAL"`
	// Timeout for database connection attempts
	CheckTimeout time.Duration `yaml:"checkTimeout" json:"check_timeout" env:"CHECK_TIMEOUT"`
	// List of namespaces to scan. Empty means all namespaces
	Namespaces []string `yaml:"namespaces" json:"namespaces" env:"NAMESPACES" envSeparator:","`

	// Concurrency settings for database connection checks
	// Controls maximum concurrent connections per database type
	MySQLConcurrency      int `yaml:"mysqlConcurrency"      json:"mysql_concurrency"      env:"MYSQL_CONCURRENCY"`
	PostgreSQLConcurrency int `yaml:"postgresqlConcurrency" json:"postgresql_concurrency" env:"POSTGRESQL_CONCURRENCY"`
	MongoDBConcurrency    int `yaml:"mongodbConcurrency"    json:"mongodb_concurrency"    env:"MONGODB_CONCURRENCY"`
	RedisConcurrency      int `yaml:"redisConcurrency"      json:"redis_concurrency"      env:"REDIS_CONCURRENCY"`
	// MySQL full-check interval. Every N checks performs full permission test;
	// other checks use lightweight basic check.
	DBCheckMod int `yaml:"dbCheckMod" json:"db_check_mod" env:"DB_CHECK_MOD"`
}

// NewDefaultConfig returns the default configuration
func NewDefaultConfig() *Config {
	return &Config{
		CheckInterval:         5 * time.Minute,
		CheckTimeout:          10 * time.Second,
		Namespaces:            []string{}, // Empty means all namespaces
		MySQLConcurrency:      50,         // Default: 50 concurrent MySQL connections
		PostgreSQLConcurrency: 50,         // Default: 50 concurrent PostgreSQL connections
		MongoDBConcurrency:    50,         // Default: 50 concurrent MongoDB connections
		RedisConcurrency:      100,        // Default: 100 concurrent Redis connections (Redis is typically faster)
		DBCheckMod:            12,         // Default: run one full MySQL check every 12 checks
	}
}
