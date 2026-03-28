package core

import "fmt"

// Get retrieves a registered block by name and asserts it to type B.
// Returns ErrBlockNotFound when the name is unknown, or a type mismatch error
// when the block exists but its concrete type does not match B.
//
// This is the preferred retrieval API when the concrete block type is known
// at the call site, as it avoids a manual type assertion in application code.
//
//	users, err := core.Get[*dynamodb.Block[User]](app, "users")
//	if err != nil { … }
//	users.PutItem(ctx, u)
func Get[B Block](c *Container, name string) (B, error) {
	var zero B
	raw, err := c.Get(name)
	if err != nil {
		return zero, err
	}
	typed, ok := raw.(B)
	if !ok {
		return zero, fmt.Errorf("go-code-blocks: block %q has unexpected type %T (want %T)", name, raw, zero)
	}
	return typed, nil
}
