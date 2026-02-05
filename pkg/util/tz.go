// Package util provides utility functions for timezone initialization.
package util

import (
	"os"
	"time"
	_ "time/tzdata"
)

// init sets the default timezone to Asia/Shanghai if TZ environment variable is not set.
// This ensures consistent time handling across different deployment environments.
func init() {
	tz := os.Getenv("TZ")
	if tz != "" {
		return
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic(err)
	}

	time.Local = loc
}
