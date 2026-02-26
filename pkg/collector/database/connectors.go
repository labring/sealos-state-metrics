package database

import (
	"errors"
	"fmt"
	"regexp"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
)

// decodeSecret decodes a base64 encoded secret value
func decodeSecret(data map[string][]byte, key string) (string, error) {
	encoded, ok := data[key]
	if !ok {
		return "", fmt.Errorf("key %s not found in secret", key)
	}

	// The data is already decoded by Kubernetes client
	return string(encoded), nil
}

// sanitizeIdentifier sanitizes a SQL identifier to prevent SQL injection
// It only allows alphanumeric characters, underscores, and hyphens
// This is used for database names, table names, etc.
func sanitizeIdentifier(identifier string) (string, error) {
	// Only allow alphanumeric, underscore, and hyphen
	re := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !re.MatchString(identifier) {
		return "", errors.New("invalid identifier: contains illegal characters")
	}

	// MySQL identifier length limit is 64 characters
	if len(identifier) > 64 {
		return "", errors.New("identifier too long: max 64 characters")
	}

	return identifier, nil
}

// quoteIdentifier quotes a SQL identifier for safe use in queries
// This is a defense-in-depth measure even after sanitization
func quoteIdentifier(identifier, dbType string) string {
	switch dbType {
	case "mysql":
		// MySQL uses backticks
		return "`" + identifier + "`"
	case "postgres":
		// PostgreSQL uses double quotes
		return `"` + identifier + `"`
	default:
		return identifier
	}
}

// buildSafeDDL constructs a DDL statement with a sanitized and quoted identifier
// This function provides an additional layer of safety and makes the security measures explicit
// Returns the SQL statement and any error from sanitization
func buildSafeDDL(template, identifier, dbType string) (string, error) {
	// Sanitize first
	sanitized, err := sanitizeIdentifier(identifier)
	if err != nil {
		return "", fmt.Errorf("identifier sanitization failed: %w", err)
	}

	// Quote the identifier
	quoted := quoteIdentifier(sanitized, dbType)

	// Build the SQL - safe because identifier has been sanitized and quoted
	// nosemgrep: go.lang.security.audit.database.string-formatted-query.string-formatted-query
	sql := fmt.Sprintf(template, quoted)

	return sql, nil
}
