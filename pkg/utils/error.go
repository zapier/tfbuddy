package utils

import (
	"errors"
	"fmt"
)

var ErrPermanent = errors.New("permanent error. cannot be retried")

type EmitHandler func(err error)

// EmitPermanentError will execute the handler if the error is Permanent and return nil.
// Otherwise it will return the passed in error.
func EmitPermanentError(err error, handler EmitHandler) error {
	if errors.Is(err, ErrPermanent) {
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
