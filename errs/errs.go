package errs

import "net/http"

type AppError struct {
	Code    int
	Message string
}

func (e AppError) Error() string {
	return e.Message
}

func NewNotFoundError(msg string) error {
	return AppError{Code: http.StatusNotFound, Message: msg}
}

func NewValidationError(msg string) error {
	return AppError{Code: http.StatusUnprocessableEntity, Message: msg}
}

func NewUnexpectedError() error {
	return AppError{Code: http.StatusInternalServerError, Message: "unexpected error"}
}
