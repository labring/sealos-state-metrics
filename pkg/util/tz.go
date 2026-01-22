package util

import (
	"os"
	"time"
	_ "time/tzdata"
)

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
