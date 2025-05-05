package control_service

import "errors"

var (
	// ErrNotFound is returned when a requested resource is not found
	ErrNotFound = errors.New("resource not found")

	// ErrInvalidInput is returned when the input data is invalid
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized is returned when the user is not authorized to perform an action
	ErrUnauthorized = errors.New("unauthorized")

	ErrInvalidNodeKey = errors.New("invalid node key")

	ErrNodeKeyExpired = errors.New("node key expired")

	ErrDatabase = errors.New("database error")
)
