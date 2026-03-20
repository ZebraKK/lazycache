package lazycache

import "errors"

var (
	// ErrNotFound is returned when a key is not found in cache and loader returns error
	ErrNotFound = errors.New("key not found")

	// ErrNoLoader is returned when Get is called without specifying a loader
	ErrNoLoader = errors.New("no loader specified")

	// ErrLoaderNotFound is returned when specified loader name doesn't exist
	ErrLoaderNotFound = errors.New("loader not found")

	// ErrLoaderPanic is returned when a loader panics during execution
	ErrLoaderPanic = errors.New("loader panicked")

	// ErrUpdateFailed is returned when a transient error (panic/timeout) prevents
	// loading a key and no stale cached value is available to fall back to.
	ErrUpdateFailed = errors.New("cache update failed")
)
