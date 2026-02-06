package imagepull

import (
	"regexp"
	"strings"
)

type FailureReason string

const (
	// FailureReasonImageNotFound indicates the image does not exist
	FailureReasonImageNotFound FailureReason = "ImageNotFound"

	// FailureReasonManifestNotFound indicates the manifest does not exist
	FailureReasonManifestNotFound FailureReason = "ManifestNotFound"

	// FailureReasonUnauthorized indicates authentication failure
	FailureReasonUnauthorized FailureReason = "Unauthorized"

	// FailureReasonRegistryUnavailable indicates registry is unreachable
	FailureReasonRegistryUnavailable FailureReason = "RegistryUnavailable"

	// FailureReasonTimeout indicates pull timeout
	FailureReasonTimeout FailureReason = "Timeout"

	// FailureReasonNetworkError indicates network error
	FailureReasonNetworkError FailureReason = "NetworkError"

	// FailureReasonDiskPressure indicates disk pressure
	FailureReasonDiskPressure FailureReason = "DiskPressure"

	// FailureReasonBackOff indicates backoff retry
	FailureReasonBackOff FailureReason = "BackOff"

	// FailureReasonUnknown indicates unknown failure
	FailureReasonUnknown FailureReason = "Unknown"
)

var (
	// Regular expressions for classifying failures
	imageNotFoundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)image.*not found`),
		regexp.MustCompile(`(?i)repository.*not found`),
		regexp.MustCompile(`(?i)404.*not found`),
	}

	manifestNotFoundPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)manifest.*not found`),
		regexp.MustCompile(`(?i)manifest.*unknown`),
	}

	unauthorizedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)unauthorized`),
		regexp.MustCompile(`(?i)authentication required`),
		regexp.MustCompile(`(?i)401`),
		regexp.MustCompile(`(?i)forbidden`),
		regexp.MustCompile(`(?i)403`),
	}

	registryUnavailablePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)registry.*unavailable`),
		regexp.MustCompile(`(?i)connection refused`),
		regexp.MustCompile(`(?i)no such host`),
		regexp.MustCompile(`(?i)dial tcp.*connection refused`),
	}

	timeoutPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)timeout`),
		regexp.MustCompile(`(?i)context deadline exceeded`),
		regexp.MustCompile(`(?i)i/o timeout`),
	}

	networkErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)network.*error`),
		regexp.MustCompile(`(?i)connection.*reset`),
		regexp.MustCompile(`(?i)broken pipe`),
		regexp.MustCompile(`(?i)EOF`),
	}
)

// FailureClassifier classifies image pull failures
type FailureClassifier struct{}

// NewFailureClassifier creates a new failure classifier
func NewFailureClassifier() *FailureClassifier {
	return &FailureClassifier{}
}

// Classify classifies the failure reason based on the error message
func (c *FailureClassifier) Classify(reason, message string) FailureReason {
	// Combine reason and message for matching
	text := reason + " " + message

	// Check reason first
	switch reason {
	case "ImagePullBackOff", "ErrImagePull":
		// BackOff is a special case - analyze the message further
		if matchesAny(text, imageNotFoundPatterns) {
			return FailureReasonImageNotFound
		}

		if matchesAny(text, manifestNotFoundPatterns) {
			return FailureReasonManifestNotFound
		}

		if matchesAny(text, unauthorizedPatterns) {
			return FailureReasonUnauthorized
		}

		if matchesAny(text, registryUnavailablePatterns) {
			return FailureReasonRegistryUnavailable
		}

		if matchesAny(text, timeoutPatterns) {
			return FailureReasonTimeout
		}

		if matchesAny(text, networkErrorPatterns) {
			return FailureReasonNetworkError
		}

		return FailureReasonBackOff
	case "ImageInspectError":
		return FailureReasonImageNotFound
	}

	// Check message patterns
	if matchesAny(text, imageNotFoundPatterns) {
		return FailureReasonImageNotFound
	}

	if matchesAny(text, manifestNotFoundPatterns) {
		return FailureReasonManifestNotFound
	}

	if matchesAny(text, unauthorizedPatterns) {
		return FailureReasonUnauthorized
	}

	if matchesAny(text, registryUnavailablePatterns) {
		return FailureReasonRegistryUnavailable
	}

	if matchesAny(text, timeoutPatterns) {
		return FailureReasonTimeout
	}

	if matchesAny(text, networkErrorPatterns) {
		return FailureReasonNetworkError
	}

	// Check for disk pressure
	if strings.Contains(strings.ToLower(text), "disk pressure") {
		return FailureReasonDiskPressure
	}

	return FailureReasonUnknown
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
