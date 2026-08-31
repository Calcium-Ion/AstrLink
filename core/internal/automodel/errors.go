package automodel

import "errors"

var (
	ErrNotFound           = errors.New("auto classifier installation was not found")
	ErrBusy               = errors.New("auto classifier installation is busy")
	ErrAlreadyInstalled   = errors.New("auto classifier is already installed")
	ErrCapacity           = errors.New("auto classifier installation limit has been reached")
	ErrLocalProbeRequired = errors.New("probe the local classifier path again before installing")
	ErrLocalSource        = errors.New("local classifier source is unavailable or unsafe")
	ErrInvalidConfig      = errors.New("classifier metadata is invalid")
	ErrUnsupportedModel   = errors.New("classifier architecture or taxonomy is unsupported")
)
