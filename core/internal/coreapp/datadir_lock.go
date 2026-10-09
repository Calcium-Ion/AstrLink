package coreapp

import "errors"

// dataDirectoryLockName is the lock file a server edition holds in its data
// directory for as long as it runs.
const dataDirectoryLockName = "core.lock"

// ErrDataDirectoryInUse means another running Core holds the data directory.
var ErrDataDirectoryInUse = errors.New("another AstrLink Core is already using this data directory")
