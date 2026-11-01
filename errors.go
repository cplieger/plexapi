package plexapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/cplieger/httpx/v5"
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

var _ httpx.Transient = (*StatusError)(nil)

// IsTransient reports a 408, a 429 or any 5xx answer, one a later attempt
// may not repeat, so httpx.IsTransient classifies a *StatusError like a
// transient transport error.
func (e *StatusError) IsTransient() bool {
	return e.Code == http.StatusRequestTimeout || e.Code == http.StatusTooManyRequests ||
		(e.Code >= 500 && e.Code <= 599)
}

// IsConfigError reports whether err is a 4xx StatusError that IsTransient
// does not report, a configuration or authorization problem that will not
// fix itself.
func IsConfigError(err error) bool {
	se, ok := errors.AsType[*StatusError](err)
	if !ok {
		return false
	}
	return se.Code >= 400 && se.Code < 500 && !se.IsTransient()
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
