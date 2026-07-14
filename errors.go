package plexapi

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when Plex answers 404 for an item lookup.
// Detect with errors.Is(err, plexapi.ErrNotFound).
var ErrNotFound = errors.New("not found")

// StatusError is returned for a non-200, non-404 Plex response, after any
// transparent retries are exhausted. It carries the status code so callers
// can classify the failure: a 4xx (bad token, wrong server) will not
// self-heal, while a 5xx (Plex still starting) may recover.
type StatusError struct {
	Method string
	Path   string
	Status string
	Code   int
}

// Error implements the error interface. The path is included (it is
// server-relative and never carries the token); the message shape follows
// the consumers' existing log grammar.
func (e *StatusError) Error() string {
	return fmt.Sprintf("plex API %s %s: %s", e.Method, e.Path, e.Status)
}

// IsFatalStartup reports whether err represents a Plex failure that will not
// self-heal and should fail startup loudly rather than retry quietly: a 4xx
// StatusError other than 408 (request timeout) and 429 (rate limit). A 5xx,
// a transport error, or any other error classifies as transient. This is the
// shared startup classifier previously duplicated (with drift) across
// consumers; TLS/certificate failures are transport errors and remain the
// caller's concern.
func IsFatalStartup(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Code == 408 || se.Code == 429 {
		return false
	}
	return se.Code >= 400 && se.Code < 500
}

// IsNotFound reports whether err is (or wraps) the ErrNotFound sentinel.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// ResponseTooLargeError is returned when a response body exceeds the
// endpoint's read cap. Carries the cap so operators can spot an unfiltered
// or oversized response class in logs.
type ResponseTooLargeError struct {
	Path  string
	Limit int64
}

// Error implements the error interface.
func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("plex API %s: response exceeds %d-byte limit", e.Path, e.Limit)
}
