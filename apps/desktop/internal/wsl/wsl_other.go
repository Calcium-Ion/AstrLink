//go:build !windows

package wsl

import "errors"

// Only Windows reaches WSL homes through their share.
func settleShareMode(string, string, bool) (bool, error) { return false, nil }

func distributions() []Distribution { return nil }

func program() (string, error) { return "", errors.New("WSL needs Windows") }

func defaultHome(distribution string) (string, error) {
	return "", errors.New("WSL distribution " + distribution + " needs Windows")
}
