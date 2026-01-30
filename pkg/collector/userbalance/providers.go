package userbalance

import (
	"context"
	"errors"
	"fmt"
	"math"
)

func (c *Collector) QueryBalance(user UserConfig) (float64, error) {
	if c.pgClient == nil {
		return 0, errors.New("database client is not initialized")
	}

	query := `
        SELECT 
            u.uid,
            u.id,
            u.name,
            COALESCE(a.balance, 0) as balance,
            COALESCE(a.deduction_balance, 0) as deduction_balance,
            COALESCE(a."encryptBalance", '') as encrypt_balance
        FROM "User" u
        LEFT JOIN "Account" a ON u.uid = a."userUid"
        WHERE u.id = $1
    `

	var (
		uid, id, name, encryptBalance string
		balance, deductionBalance     int64
	)

	err := c.pgClient.QueryRow(context.Background(), query, user.UID).Scan(
		&uid,
		&id,
		&name,
		&balance,
		&deductionBalance,
		&encryptBalance,
	)
	if err != nil {
		return 0, fmt.Errorf("query user balance failed: %w", err)
	}

	actualBalance := math.Round(float64(balance-deductionBalance)/1000000*100) / 100

	return actualBalance, nil
}
