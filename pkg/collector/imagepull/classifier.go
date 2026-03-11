package imagepull

import (
	"regexp"
	"strings"
)

type FailureReason string

const (
	// FailureReasonImageNotFound indicates the image does not exist
	FailureReasonImageNotFound FailureReason = "image_not_found"

	// FailureReasonProxyError indicates a proxy connectivity error.
	FailureReasonProxyError FailureReason = "proxy_error"

	// FailureReasonUnauthorized indicates authentication failure
	FailureReasonUnauthorized FailureReason = "unauthorized"

	// FailureReasonTLSHandshake indicates a TLS handshake/certificate failure.
	FailureReasonTLSHandshake FailureReason = "tls_handshake_error"

	// FailureReasonIOTimeout indicates a network I/O timeout while pulling.
	FailureReasonIOTimeout FailureReason = "io_timeout"

	// FailureReasonConnectionRefused indicates the registry actively refused the connection.
	FailureReasonConnectionRefused FailureReason = "connection_refused"

	// FailureReasonNetworkError indicates network error
	FailureReasonNetworkError FailureReason = "network_error"

	// FailureReasonBackOff indicates backoff retry
	FailureReasonBackOff FailureReason = "back_off_pulling_image"

	// FailureReasonUnknown indicates unknown failure
	FailureReasonUnknown FailureReason = "unknown"
)

var (
	// Regular expressions for classifying failures
	reImageNotFound = regexp.MustCompile(
		`(?i)not found|NotFound|manifest unknown|repository does not exist`,
	)
	reProxyError   = regexp.MustCompile(`(?i)proxyconnect|proxy error`)
	reUnauthorized = regexp.MustCompile(
		`(?i)unauthorized|authentication require|failed to authorize|authorization failed`,
	)
	reTLS               = regexp.MustCompile(`(?i)tls handshake|failed to verify certificate`)
	reIOTimeout         = regexp.MustCompile(`(?i)i/o timeout`)
	reConnectionRefused = regexp.MustCompile(`(?i)connection refused`)
	reNetworkError      = regexp.MustCompile(`(?i)failed to do request`)
)

// FailureClassifier classifies image pull failures.
type FailureClassifier struct{}

// NewFailureClassifier creates a new failure classifier
func NewFailureClassifier() *FailureClassifier {
	return &FailureClassifier{}
}

// Classify classifies the failure reason based on the error message.
// It intentionally mirrors the legacy main.go.back behavior so that
// ImagePullBackOff does not overwrite a previously more specific root cause.
func (c *FailureClassifier) Classify(reason, message string) FailureReason {
	lowMsg := strings.ToLower(message)
	switch strings.ToLower(reason) {
	case "errimagepull", "imagepullbackoff":
		if reImageNotFound.MatchString(lowMsg) {
			return FailureReasonImageNotFound
		}

		if reProxyError.MatchString(lowMsg) {
			return FailureReasonProxyError
		}

		if reUnauthorized.MatchString(lowMsg) {
			return FailureReasonUnauthorized
		}

		if reTLS.MatchString(lowMsg) {
			return FailureReasonTLSHandshake
		}

		if reIOTimeout.MatchString(lowMsg) {
			return FailureReasonIOTimeout
		}

		if reConnectionRefused.MatchString(lowMsg) {
			return FailureReasonConnectionRefused
		}

		if reNetworkError.MatchString(lowMsg) {
			return FailureReasonNetworkError
		}

		if strings.HasPrefix(lowMsg, "back-off pulling image") {
			return FailureReasonBackOff
		}

		return FailureReasonUnknown
	default:
		return FailureReason(strings.ToLower(reason))
	}
}

func isSpecificReason(reason FailureReason) bool {
	return reason != FailureReasonBackOff && reason != FailureReasonUnknown
}
