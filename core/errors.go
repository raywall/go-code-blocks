package core

import "errors"

var (
	// ErrBlockNotFound is returned when Get cannot find a registered block by name.
	ErrBlockNotFound = errors.New("go-code-blocks: block not found")

	// ErrNotInitialized is returned when a block operation is called before Init.
	ErrNotInitialized = errors.New("go-code-blocks: block not initialized; call Init first")

	// ErrItemNotFound is returned when a requested item does not exist.
	ErrItemNotFound = errors.New("go-code-blocks: item not found")

	// ErrAlreadyRegistered is returned when registering a duplicate block name.
	ErrAlreadyRegistered = errors.New("go-code-blocks: block already registered")
)
