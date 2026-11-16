package utils

import (
	"errors"
	"fmt"
	"testing"
)

func TestWrappedPermanentErr(t *testing.T) {
	first := fmt.Errorf("could not process: %w", ErrPermanent)
	second := fmt.Errorf("failed to write %w", first)

	if !errors.Is(second, ErrPermanent) {
		t.Fatal("expected error to be Permanent")
	}
	t.Log(second.Error())
}

func TestEmitPermanentError(t *testing.T) {
	t.Run("handle permanent error", func(t *testing.T) {
		baseErr := fmt.Errorf("could not send message after retries: %w", ErrPermanent)
		retVal := EmitPermanentError(baseErr, func(err error) {
			if err.Error() != "could not send message after retries: "+ErrPermanent.Error() {
				t.Error("expected wrapped error")
			}
		})
		if retVal != nil {
			t.Error("expected returned error to be nil")
		}
	})

	t.Run("normal error", func(t *testing.T) {
		normalErr := errors.New("some temporary error")
		retErr := EmitPermanentError(normalErr, func(err error) {
			t.Error("this should not be called")
		})
		if !errors.Is(normalErr, retErr) {
			t.Error("expected retErr to be normalErr")
		}

	})

	t.Run("handle nil error", func(t *testing.T) {
		retErr := EmitPermanentError(nil, func(err error) {
			t.Error("this should not be called")
		})
		if retErr != nil {
			t.Error("expected retErr to be nil")
		}
	})

}

func TestCreatePermanentError(t *testing.T) {
	t.Run("handle nil error", func(t *testing.T) {
		err := CreatePermanentError(nil)
		if err != nil {
			t.Error("expected error to be nil")
		}
	})
	t.Run("wrap error as permanent", func(t *testing.T) {
		baseErr := errors.New("test error")
		err := CreatePermanentError(baseErr)
		if !errors.Is(err, ErrPermanent) {
			t.Error("expected returned error to be permanent")
		}
	})
}

// The permanent error keeps the API client's error, so callers can still
// tell, for example, a 404 apart. The message is unchanged.
func TestCreatePermanentHTTPErrorKeepsTheCause(t *testing.T) {
	cause := errors.New("404 Not Found")
	err := CreatePermanentHTTPError(404, cause)
	if !errors.Is(err, ErrPermanent) {
		t.Error("expected a permanent error")
	}
	if !errors.Is(err, cause) {
		t.Error("expected the error to wrap its cause")
	}
	if want := "404 Not Found " + ErrPermanent.Error(); err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}
