package plexapi

import (
	"errors"
	"fmt"
)

// ErrNotFound is returned when Plex answers 404 for an item lookup.
var ErrNotFound = errors.New("not found")

// StatusError is returned for a non-200, non-404 Plex response, after any
// transparent retries are exhausted.
type StatusError struct {
	Method string
	Path   string
	Status string
	Code   int
}

// Error implements the error interface.
func (e *StatusError) Error() string {
	return fmt.Sprintf("plex API %s %s: %s", e.Method, e.Path, e.Status)
}

// IsConfigError reports whether err is a Plex response indicating a
// configuration or authorization problem — a 4xx StatusError other than 408
// (request timeout) and 429 (rate limit, retried transparently).
func IsConfigError(err error) bool {
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
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
// endpoint's read cap.
type ResponseTooLargeError struct {
	Path  string
	Limit int64
}

// Error implements the error interface.
func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("plex API %s: response exceeds %d-byte limit", e.Path, e.Limit)
}
