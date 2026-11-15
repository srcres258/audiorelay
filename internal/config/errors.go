package config

import "errors"

// ErrRequired indicates that a required configuration value was omitted.
var ErrRequired = errors.New("value is required")

// ErrInvalid indicates that a configuration value is malformed or unsupported.
var ErrInvalid = errors.New("invalid configuration")
