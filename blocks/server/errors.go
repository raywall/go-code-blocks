// blocks/server/errors.go
package server

import "errors"

var (
	// errNoHandler is returned when neither WithRouter nor WithHandler was provided.
	errNoHandler = errors.New("server: no router or handler configured; use WithRouter or WithHandler")
)
