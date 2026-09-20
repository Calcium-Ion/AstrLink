//go:build !windows

package coreapp

import "syscall"

const addressInUse = syscall.EADDRINUSE
