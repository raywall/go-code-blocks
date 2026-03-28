package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Container manages the registration and lifecycle of all blocks.
// Blocks are initialized in registration order and shut down in reverse order,
// following standard dependency teardown conventions.
type Container struct {
	mu     sync.RWMutex
	blocks []Block
	index  map[string]Block
}

// NewContainer creates a new, empty Container.
func NewContainer() *Container {
	return &Container{index: make(map[string]Block)}
}

// Register adds a block to the container.
// Returns ErrAlreadyRegistered if a block with the same name exists.
func (c *Container) Register(b Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.index[b.Name()]; exists {
		return fmt.Errorf("%w: %q", ErrAlreadyRegistered, b.Name())
	}
	c.blocks = append(c.blocks, b)
	c.index[b.Name()] = b
	return nil
}

// MustRegister is like Register but panics on error.
// Useful for wiring blocks at startup where an error is unrecoverable.
func (c *Container) MustRegister(b Block) {
	if err := c.Register(b); err != nil {
		panic(err)
	}
}

// Get retrieves a registered block by name.
// Returns ErrBlockNotFound if no block with that name exists.
func (c *Container) Get(name string) (Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	b, ok := c.index[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrBlockNotFound, name)
	}
	return b, nil
}

// InitAll initializes all registered blocks in registration order.
// Stops and returns on the first error.
func (c *Container) InitAll(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, b := range c.blocks {
		if err := b.Init(ctx); err != nil {
			return fmt.Errorf("go-code-blocks: init %q: %w", b.Name(), err)
		}
	}
	return nil
}

// ShutdownAll shuts down all registered blocks in reverse order, collecting
// all errors and joining them so partial failures are visible.
func (c *Container) ShutdownAll(ctx context.Context) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var errs []error
	for i := len(c.blocks) - 1; i >= 0; i-- {
		b := c.blocks[i]
		if err := b.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("shutdown %q: %w", b.Name(), err))
		}
	}
	return errors.Join(errs...)
}
