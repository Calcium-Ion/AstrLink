package coreapp

import "golang.org/x/sys/windows"

// Winsock bind errors use WSA codes, not syscall.EADDRINUSE.
const addressInUse = windows.WSAEADDRINUSE
