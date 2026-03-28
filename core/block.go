package core

import "context"

// Block defines the lifecycle contract that every integration block must fulfill.
// Blocks are self-contained units that encapsulate a single external dependency
// (DynamoDB, S3, Redis, etc.) and expose a typed API over it.
type Block interface {
	// Name returns the unique identifier used to register and retrieve this block.
	Name() string
	// Init establishes connections, validates configuration, and readies the block.
	// It is called once during application startup.
	Init(ctx context.Context) error
	// Shutdown releases all resources held by the block.
	// It is called in reverse-registration order during graceful shutdown.
	Shutdown(ctx context.Context) error
}
