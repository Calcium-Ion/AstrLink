// Package core supervises astrlink-core, the gateway the desktop app runs as
// a child process: its command line and the three secret lines on its stdin,
// the ready announcement on its stdout, the health and contract checks, the
// recovery of a Core that exited, and the stop that waits for it, as the
// Tauri shell did.
package core
