package utils

import "errors"

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
