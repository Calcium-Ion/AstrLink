// Package instance names the AstrLink this process is: the installed one, or
// the isolated development instance `mygo dev` runs beside it. The two keep
// the data and config directories, link scheme, default inference port and
// App Group the Tauri shell gave them, so neither loses its data on the move
// to MyGo.
package instance
