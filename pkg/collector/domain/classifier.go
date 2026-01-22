// Package domain provides domain health check metrics collection
package domain

import (
	"regexp"
	"strings"
)

// ErrorType represents the classified type of domain check error
type ErrorType string

const (
	// HTTP errors
	ErrorTypeConnectionRefused    ErrorType = "ConnectionRefused"
	ErrorTypeTimeout              ErrorType = "Timeout"
	ErrorTypeDNSError             ErrorType = "DNSError"
	ErrorTypeHTTPClientError      ErrorType = "HTTPClientError"  // 4xx
	ErrorTypeHTTPServerError      ErrorType = "HTTPServerError"  // 5xx
	ErrorTypeSSLError             ErrorType = "SSLError"
	ErrorTypeNetworkError         ErrorType = "NetworkError"

	// Certificate errors
	ErrorTypeCertExpired          ErrorType = "CertExpired"
	ErrorTypeCertNotFound         ErrorType = "CertNotFound"
	ErrorTypeCertInvalid          ErrorType = "CertInvalid"
	ErrorTypeCertHostnameMismatch ErrorType = "CertHostnameMismatch"

	// Unknown
	ErrorTypeUnknown              ErrorType = "Unknown"
	ErrorTypeNone                 ErrorType = "None"  // No error
)

var (
	connectionRefusedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)connection refused`),
		regexp.MustCompile(`(?i)dial tcp.*connection refused`),
	}

	timeoutPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)timeout`),
		regexp.MustCompile(`(?i)context deadline exceeded`),
		regexp.MustCompile(`(?i)i/o timeout`),
		regexp.MustCompile(`(?i)request timeout`),
	}

	dnsErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)no such host`),
		regexp.MustCompile(`(?i)dns.*fail`),
		regexp.MustCompile(`(?i)lookup.*fail`),
		regexp.MustCompile(`(?i)name resolution fail`),
	}

	httpClientErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)40[0-9]`),
		regexp.MustCompile(`(?i)not found`),
		regexp.MustCompile(`(?i)unauthorized`),
		regexp.MustCompile(`(?i)forbidden`),
	}

	httpServerErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)50[0-9]`),
		regexp.MustCompile(`(?i)internal server error`),
		regexp.MustCompile(`(?i)bad gateway`),
		regexp.MustCompile(`(?i)service unavailable`),
	}

	sslErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)ssl.*error`),
		regexp.MustCompile(`(?i)tls.*handshake`),
		regexp.MustCompile(`(?i)certificate.*verify`),
	}

	networkErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)network.*unreachable`),
		regexp.MustCompile(`(?i)connection.*reset`),
		regexp.MustCompile(`(?i)broken pipe`),
		regexp.MustCompile(`(?i)EOF`),
	}

	certExpiredPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)certificate.*expired`),
		regexp.MustCompile(`(?i)cert.*expired`),
	}

	certNotFoundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)certificate.*not found`),
		regexp.MustCompile(`(?i)no.*certificate`),
	}

	certInvalidPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)certificate.*invalid`),
		regexp.MustCompile(`(?i)invalid.*certificate`),
		regexp.MustCompile(`(?i)bad certificate`),
	}

	certHostnameMismatchPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)hostname.*mismatch`),
		regexp.MustCompile(`(?i)certificate.*valid.*name`),
	}
)

// ErrorClassifier classifies domain check errors
type ErrorClassifier struct{}

// NewErrorClassifier creates a new error classifier
func NewErrorClassifier() *ErrorClassifier {
	return &ErrorClassifier{}
}

// ClassifyHTTPError classifies HTTP check error
func (c *ErrorClassifier) ClassifyHTTPError(errorMsg string) ErrorType {
	if errorMsg == "" {
		return ErrorTypeNone
	}

	text := strings.ToLower(errorMsg)

	if matchesAny(text, connectionRefusedPatterns) {
		return ErrorTypeConnectionRefused
	}

	if matchesAny(text, timeoutPatterns) {
		return ErrorTypeTimeout
	}

	if matchesAny(text, dnsErrorPatterns) {
		return ErrorTypeDNSError
	}

	if matchesAny(text, httpClientErrorPatterns) {
		return ErrorTypeHTTPClientError
	}

	if matchesAny(text, httpServerErrorPatterns) {
		return ErrorTypeHTTPServerError
	}

	if matchesAny(text, sslErrorPatterns) {
		return ErrorTypeSSLError
	}

	if matchesAny(text, networkErrorPatterns) {
		return ErrorTypeNetworkError
	}

	return ErrorTypeUnknown
}

// ClassifyCertError classifies certificate check error
func (c *ErrorClassifier) ClassifyCertError(errorMsg string) ErrorType {
	if errorMsg == "" {
		return ErrorTypeNone
	}

	text := strings.ToLower(errorMsg)

	if matchesAny(text, certExpiredPatterns) {
		return ErrorTypeCertExpired
	}

	if matchesAny(text, certNotFoundPatterns) {
		return ErrorTypeCertNotFound
	}

	if matchesAny(text, certHostnameMismatchPatterns) {
		return ErrorTypeCertHostnameMismatch
	}

	if matchesAny(text, certInvalidPatterns) {
		return ErrorTypeCertInvalid
	}

	// SSL errors might also be cert-related
	if matchesAny(text, sslErrorPatterns) {
		return ErrorTypeSSLError
	}

	return ErrorTypeUnknown
}

// matchesAny checks if the text matches any of the patterns
func matchesAny(text string, patterns []*regexp.Regexp) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}

	return false
}
