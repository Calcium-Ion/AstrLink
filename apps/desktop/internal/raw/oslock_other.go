//go:build !linux

package raw

import "context"

// watchSession has nothing to follow beyond MyGo's Power events here.
func watchSession(context.Context, func(reason string)) {}
