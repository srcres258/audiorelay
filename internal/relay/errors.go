package relay

import "errors"

// ErrNilBackend indicates that no pipeline backend was provided.
var ErrNilBackend = errors.New("relay: nil pipeline backend")
