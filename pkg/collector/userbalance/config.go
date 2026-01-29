package userbalance

import (
	"time"
)

// DatabaseConfig holds database connection configuration
type DatabaseConfig struct {
	DSN string `yaml:"dsn" json:"dsn"` // Database connection string
}

// UserConfig holds configuration for a single user/account
type UserConfig struct {
	Region string `yaml:"region" json:"region"` // Cloud service region
	UUID   string `yaml:"uuid"   json:"uuid"`   // User unique identifier
	Uid    string `yaml:"uid"    json:"uid"`    // User ID
	Owner  string `yaml:"owner"  json:"owner"`  // Account owner
	Type   string `yaml:"type"   json:"type"`   // User type
	Level  string `yaml:"level"  json:"level"`  // User level
}

// Config contains configuration for the UserBalance collector
type Config struct {
	DatabaseConfig DatabaseConfig `yaml:"database"      json:"database"`                            // Database configuration
	UserConfig     []UserConfig   `yaml:"users"         json:"users"`                               // User configurations list
	CheckInterval  time.Duration  `yaml:"checkInterval" json:"check_interval" env:"CHECK_INTERVAL"` // Check interval duration
}

// NewDefaultConfig returns the default configuration for UserBalance collector
func NewDefaultConfig() *Config {
	return &Config{
		DatabaseConfig: DatabaseConfig{
			DSN: "",
		},
		UserConfig:    []UserConfig{},
		CheckInterval: 5 * time.Minute,
	}
}
