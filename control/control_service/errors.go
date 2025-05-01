package control_service

import "errors"

var (
	// ErrNotFound is returned when a requested resource is not found
	ErrNotFound = errors.New("resource not found")

	// ErrInvalidInput is returned when the input data is invalid
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized is returned when the user is not authorized to perform an action
	ErrUnauthorized = errors.New("unauthorized")

	// Add more business-specific errors as needed
	ErrInvalidNodeKey = errors.New("invalid node key")

	ErrDatabase = errors.New("database error")
)
