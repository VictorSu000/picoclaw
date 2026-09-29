package providers

import (
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"

	"github.com/sipeed/picoclaw/pkg/providers/common"
)

// Common patterns in Go HTTP error messages
var httpStatusPatterns = []*regexp.Regexp{
	regexp.MustCompile(`status[:\s]+(\d{3})`),
	regexp.MustCompile(`http[/\s]+\d*\.?\d*\s+(\d{3})`),
	regexp.MustCompile(`\b([3-5]\d{2})\b`),
}

// errorPattern defines a single pattern (string or regex) for error classification.
type errorPattern struct {
	substring string
	regex     *regexp.Regexp
}

func substr(s string) errorPattern { return errorPattern{substring: s} }
func rxp(r string) errorPattern    { return errorPattern{regex: regexp.MustCompile("(?i)" + r)} }

// Error patterns organized by FailoverReason, matching OpenClaw production (~40 patterns).
var (
	rateLimitPatterns = []errorPattern{
		rxp(`rate[_ ]limit`),
		substr("too many requests"),
		substr("429"),
		substr("exceeded your current quota"),
		rxp(`exceeded.*quota`),
		rxp(`resource has been exhausted`),
		rxp(`resource.*exhausted`),
		substr("resource_exhausted"),
		substr("quota exceeded"),
		substr("usage limit"),
	}

	overloadedPatterns = []errorPattern{
		rxp(`overloaded_error`),
		rxp(`"type"\s*:\s*"overloaded_error"`),
		substr("overloaded"),
	}

	timeoutPatterns = []errorPattern{
		substr("timeout"),
		substr("timed out"),
		substr("deadline exceeded"),
		substr("context deadline exceeded"),
	}

	networkPatterns = []errorPattern{
		substr("connection reset"),
		substr("reset by peer"),
		substr("connection refused"),
		substr("connection aborted"),
		substr("broken pipe"),
		substr("use of closed network connection"),
		substr("network is unreachable"),
		substr("host is unreachable"),
		substr("no such host"),
		substr("temporary failure in name resolution"),
		substr("server misbehaving"),
		substr("read tcp"),
		substr("write tcp"),
		substr("dial tcp"),
		substr("tls:"),
		substr("x509:"),
		substr("certificate"),
		substr("handshake"),
		substr("unexpected eof"),
		substr("read: eof"),
		substr("write: eof"),
	}

	billingPatterns = []errorPattern{
		rxp(`\b402\b`),
		substr("payment required"),
		substr("insufficient credits"),
		substr("credit balance"),
		substr("plans & billing"),
		substr("insufficient balance"),
	}

	authPatterns = []errorPattern{
		rxp(`\b(?:invalid|incorrect|malformed|wrong)[-_\s]+(?:api[-_\s]*)?key\b`),
		rxp(`\b(?:api[-_\s]*)?key[-_\s]+(?:is[-_\s]+)?(?:invalid|incorrect|malformed|wrong)\b`),
		rxp(`invalid[_ ]?api[_ ]?key`),
		substr("incorrect api key"),
		substr("invalid token"),
		substr("authentication"),
		substr("re-authenticate"),
		substr("oauth token refresh failed"),
		substr("unauthorized"),
		substr("forbidden"),
		substr("access denied"),
		substr("expired"),
		substr("token has expired"),
		rxp(`\b401\b`),
		rxp(`\b403\b`),
		substr("no credentials found"),
		substr("no api key found"),
	}

	formatPatterns = []errorPattern{
		substr("string should match pattern"),
		substr("tool_use.id"),
		substr("tool_use_id"),
		substr("messages.1.content.1.tool_use.id"),
		substr("invalid request format"),
		// Zhipu API error code 1210: parameter error (e.g., image format incompatible)
		substr("error code: 1210"),
		substr("error code 1210"),
		substr("zhipu api error code: 1210"),
	}
	contextOverflowPatterns = []errorPattern{
		rxp(`context[_ ]?length[_ ]?exceeded`),
		rxp(`context[_ ]?window[_ ]?exceeded`),
		substr("maximum context length"),
		substr("token limit"),
		substr("too many tokens"),
		substr("prompt is too long"),
		substr("request too large"),
	}

	// In-band failures arrive inside HTTP 200 bodies, so the provider has
	// already told us the request produced no completion. Only the cases where
	// resending the identical request cannot possibly help are listed here;
	// everything else is treated as transient by classifyInBandError.
	// Enumerating the retryable side instead would break every time a gateway
	// introduces a new error code.
	inBandFormatPatterns = []errorPattern{
		rxp(`invalid[_ ]request[_ ]error`),
		rxp(`unsupported[_ ](?:parameter|model|feature|value)`),
		rxp(`invalid[_ ]model`),
		rxp(`model[_ ](?:not[_ ]found|does[_ ]not[_ ]exist)`),
		rxp(`content[_ ]filter`),
		rxp(`content[_ ]policy`),
		// A non-numeric code never reaches here when it parses as a status, but
		// gateways sometimes spell the status out in the message text.
		rxp(`\b(?:400|404|405|413|415|422)\b`),
	}

	inBandAuthPatterns = []errorPattern{
		rxp(`invalid[_ ]api[_ ]key`),
		rxp(`authentication[_ ]error`),
		rxp(`permission[_ ]error`),
		rxp(`permission[_ ]denied`),
		substr("invalid api key"),
		substr("unauthorized"),
		substr("forbidden"),
		rxp(`\b(?:401|403)\b`),
	}

	inBandBillingPatterns = []errorPattern{
		rxp(`insufficient[_ ]quota`),
		rxp(`billing`),
		rxp(`\b402\b`),
		substr("payment required"),
		substr("insufficient credits"),
	}

	imageDimensionPatterns = []errorPattern{
		rxp(`image dimensions exceed max`),
	}

	imageSizePatterns = []errorPattern{
		rxp(`image exceeds.*mb`),
	}

	// Transient HTTP status codes that map to timeout (server-side failures).
	transientStatusCodes = map[int]bool{
		500: true, 502: true, 503: true,
		521: true, 522: true, 523: true, 524: true,
		529: true,
	}
)

// ClassifyError classifies an error into a FailoverError with reason.
// Returns nil if the error is not classifiable (unknown errors should not trigger fallback).
func ClassifyError(err error, provider, model string) *FailoverError {
	if err == nil {
		return nil
	}

	// Context cancellation: user abort, never fallback.
	if err == context.Canceled {
		return nil
	}

	// Context deadline exceeded: treat as timeout, always fallback.
	if err == context.DeadlineExceeded {
		return &FailoverError{
			Reason:   FailoverTimeout,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	msg := strings.ToLower(err.Error())

	// Concrete transport errors should continue the fallback chain even when
	// providers do not expose a structured HTTP status.
	if reason := classifyByErrorType(err); reason != "" {
		return &FailoverError{
			Reason:   reason,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Image dimension/size errors: non-retriable, non-fallback.
	if IsImageDimensionError(msg) || IsImageSizeError(msg) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Try HTTP status code extraction first.
	var httpErr *common.HTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		if reason := classifyByStatus(httpErr.StatusCode); reason != "" {
			return &FailoverError{
				Reason:   reason,
				Provider: provider,
				Model:    model,
				Status:   httpErr.StatusCode,
				Wrapped:  err,
			}
		}
	}
	// In-band errors embedded in HTTP 200 bodies carry their own status code.
	var inBandErr *common.InBandAPIError
	if errors.As(err, &inBandErr) && inBandErr != nil {
		// A numeric code that maps onto a known HTTP status wins outright.
		if code := inBandErr.StatusCode(); code > 0 {
			if reason := classifyByStatus(code); reason != "" {
				return &FailoverError{
					Reason:   reason,
					Provider: provider,
					Model:    model,
					Status:   code,
					Wrapped:  err,
				}
			}
		}
		// Otherwise the code is non-numeric ("upstream_error") or is not a
		// status at all (42.5, 999, 200). Classify from the error's own fields
		// so the failure still reaches the retry and fallback paths.
		if reason := classifyInBandError(inBandErr); reason != "" {
			return &FailoverError{
				Reason:   reason,
				Provider: provider,
				Model:    model,
				Status:   inBandErr.StatusCode(),
				Wrapped:  err,
			}
		}
	}
	if status := extractHTTPStatus(msg); status > 0 {
		if reason := classifyByStatus(status); reason != "" {
			return &FailoverError{
				Reason:   reason,
				Provider: provider,
				Model:    model,
				Status:   status,
				Wrapped:  err,
			}
		}
	}

	// Message pattern matching (priority order from OpenClaw).
	if reason := classifyByMessage(msg); reason != "" {
		return &FailoverError{
			Reason:   reason,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	return nil
}

// classifyByErrorType maps concrete transport-layer error types to a retryable
// fallback reason before message heuristics are applied.
func classifyByErrorType(err error) FailoverReason {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return FailoverNetwork
	}

	for _, transportErr := range []error{
		syscall.ECONNRESET,
		syscall.ECONNABORTED,
		syscall.ECONNREFUSED,
		syscall.ETIMEDOUT,
		syscall.EHOSTUNREACH,
		syscall.ENETUNREACH,
		syscall.EPIPE,
	} {
		if errors.Is(err, transportErr) {
			if transportErr == syscall.ETIMEDOUT {
				return FailoverTimeout
			}
			return FailoverNetwork
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return FailoverTimeout
		}
		return FailoverNetwork
	}

	return ""
}

// classifyInBandError classifies an HTTP 200 body error whose "code" field is
// not a numeric status.
//
// The provider reported the error inside a successful HTTP response, which
// means no usable completion was produced. Because gateway error taxonomies
// change constantly and cannot be enumerated up front, this fails open: only
// failures that a plain resend provably cannot fix are rejected, and every
// other code is treated as a transient upstream fault so it still reaches the
// retry and fallback paths.
func classifyInBandError(e *common.InBandAPIError) FailoverReason {
	if e == nil {
		return ""
	}
	fields := []string{e.Type, e.Message}
	if s, ok := e.Code.(string); ok {
		fields = append(fields, s)
	}
	msg := strings.ToLower(strings.Join(fields, " "))

	switch {
	// Needs history compaction, not a blind resend.
	case matchesAny(msg, contextOverflowPatterns):
		return FailoverContextOverflow
	// Resending the identical request will fail identically.
	case matchesAny(msg, inBandFormatPatterns):
		return FailoverFormat
	// Same credentials, same outcome; only a different candidate can help.
	case matchesAny(msg, inBandAuthPatterns):
		return FailoverAuth
	case matchesAny(msg, inBandBillingPatterns):
		return FailoverBilling
	// Kept explicit so the retry event is labelled rate_limit rather than
	// server_error, and so the candidate lands in a rate-limit cooldown.
	case matchesAny(msg, rateLimitPatterns):
		return FailoverRateLimit
	}
	return FailoverTimeout
}

// classifyByStatus maps HTTP status codes to FailoverReason.
func classifyByStatus(status int) FailoverReason {
	switch {
	case status == 401 || status == 403:
		return FailoverAuth
	case status == 402:
		return FailoverBilling
	case status == 408:
		return FailoverTimeout
	case status == 429:
		return FailoverRateLimit
	case status == 400:
		return FailoverFormat
	case transientStatusCodes[status]:
		return FailoverTimeout
	}
	return ""
}

// classifyByMessage matches error messages against patterns.
// Priority order matters (from OpenClaw classifyFailoverReason).
func classifyByMessage(msg string) FailoverReason {
	if matchesAny(msg, rateLimitPatterns) {
		return FailoverRateLimit
	}
	if matchesAny(msg, overloadedPatterns) {
		return FailoverRateLimit // Overloaded treated as rate_limit
	}
	if matchesAny(msg, billingPatterns) {
		return FailoverBilling
	}
	if matchesAny(msg, timeoutPatterns) {
		return FailoverTimeout
	}
	if matchesAny(msg, networkPatterns) {
		return FailoverNetwork
	}
	if matchesAny(msg, authPatterns) {
		return FailoverAuth
	}
	if matchesAny(msg, formatPatterns) {
		return FailoverFormat
	}
	if matchesAny(msg, contextOverflowPatterns) {
		return FailoverContextOverflow
	}
	return ""
}

// extractHTTPStatus extracts an HTTP status code from an error message.
// Looks for patterns like "status: 429", "status 429", "http/1.1 429", "http 429", or standalone "429".
func extractHTTPStatus(msg string) int {
	for _, p := range httpStatusPatterns {
		if m := p.FindStringSubmatch(msg); len(m) > 1 {
			return parseDigits(m[1])
		}
	}
	return 0
}

// IsImageDimensionError returns true if the message indicates an image dimension error.
func IsImageDimensionError(msg string) bool {
	return matchesAny(msg, imageDimensionPatterns)
}

// IsImageSizeError returns true if the message indicates an image file size error.
func IsImageSizeError(msg string) bool {
	return matchesAny(msg, imageSizePatterns)
}

// matchesAny checks if msg matches any of the patterns.
func matchesAny(msg string, patterns []errorPattern) bool {
	for _, p := range patterns {
		if p.regex != nil {
			if p.regex.MatchString(msg) {
				return true
			}
		} else if p.substring != "" {
			if strings.Contains(msg, p.substring) {
				return true
			}
		}
	}
	return false
}

// parseDigits converts a string of digits to an int.
func parseDigits(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
