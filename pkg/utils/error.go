package utils

import (
	"errors"
	"fmt"
	"net/http"
)

var ErrPermanent = errors.New("permanent error. cannot be retried")

type EmitHandler func(err error)

// EmitPermanentError will execute the handler if the error is Permanent and return nil.
// Otherwise it will return the passed in error.
func EmitPermanentError(err error, handler EmitHandler) error {
	if errors.Is(err, ErrPermanent) {
		handler(err)
		return nil
	}
	return err
}

// CreatePermanentError will return a permanent error with the string contents of the passed in error
func CreatePermanentError(err error) error {
	if err == nil {
		return nil
	}
	// Go 1.20 will support wrapping multiple errors
	return fmt.Errorf("%s %w", err.Error(), ErrPermanent)
}

// CreatePermanentHTTPError will return a permanent error if the status code it's not a retryable status code.
func CreatePermanentHTTPError(statusCode int, err error) error {
	if err == nil || statusCode < 400 {
		return nil
	}
	switch statusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return err
	default:
		return fmt.Errorf("%s %w", err.Error(), ErrPermanent)
	}
}
