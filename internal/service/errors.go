package service

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid input")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrEmailNotVerified  = errors.New("email is not verified")
	ErrOutOfStock        = errors.New("insufficient stock")
	ErrInvalidTransition = errors.New("invalid order status transition")
)
