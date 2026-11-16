package pipeline

import "errors"

// ErrAlreadyStarted indicates that Start was called more than once.
var ErrAlreadyStarted = errors.New("pipeline: already started")

// ErrNotStarted indicates that a lifecycle method requiring a running process
// was called before Start.
var ErrNotStarted = errors.New("pipeline: not started")
