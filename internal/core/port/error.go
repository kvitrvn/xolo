package port

import "errors"

var (
	ErrNotFound      = errors.New("not found")
	ErrCanceled      = errors.New("canceled")
	ErrAlreadyExists = errors.New("already exists")
	ErrNotAllowed    = errors.New("not allowed")

	// ErrInvalid signals a syntactically well-formed value that the domain
	// refuses: an unknown permission code, a role belonging to another
	// organization, a malformed slug...
	ErrInvalid = errors.New("invalid")
)
