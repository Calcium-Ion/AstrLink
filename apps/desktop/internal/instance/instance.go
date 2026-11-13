package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
)

const (
	// Identifier is the installed AstrLink's.
	Identifier = "com.astrlink.desktop"
	// IsolatedIdentifier is the development instance's, which the Tauri
	// shell gave `make dev` and its data directories are named after.
	IsolatedIdentifier = "com.astrlink.desktop.isolated"
	Name               = "AstrLink"
	IsolatedName       = "AstrLink Dev"
	LinkScheme         = "astrlink"
	IsolatedLinkScheme = "astrlink-isolated"
	// teamID prefixes the App Groups the macOS widget reads.
	teamID = "7XWQYGN832"

	defaultInferencePort         uint16 = 18317
	isolatedDefaultInferencePort uint16 = 18318
)

// Instance is the AstrLink this process is.
type Instance struct {
	// Isolated is the development instance `mygo dev` runs beside the
	// installed one, with its own data, port and link scheme. Client
	// configs, agent tools and the agent locator stay in the user's home,
	// shared with the other AstrLink.
	Isolated bool
}

// Current is the isolated development instance or the installed one.
func Current(isolated bool) Instance { return Instance{Isolated: isolated} }

// FromEnvironment is the instance this process is: the isolated one when
// `mygo dev` started it, which it does with MYGO_ENV=development, and the
// installed one otherwise, however it was built. `mygo build -debug` makes a
// development build that still runs as the installed AstrLink, as the Tauri
// shell's debug builds did; only the bundle `mygo dev` makes declares the
// isolated link scheme, icon and App Group (mygo.config.ts).
func FromEnvironment() Instance { return fromEnvironment(os.Getenv) }

func fromEnvironment(getenv func(string) string) Instance {
	return Current(getenv("MYGO_ENV") == "development")
}

// Identifier is the identifier the instance's directories are named after.
func (i Instance) Identifier() string {
	if i.Isolated {
		return IsolatedIdentifier
	}
	return Identifier
}

// Name is the product name the instance shows.
func (i Instance) Name() string {
	if i.Isolated {
		return IsolatedName
	}
	return Name
}

// LinkScheme is the scheme of the instance's import links.
func (i Instance) LinkScheme() string {
	if i.Isolated {
		return IsolatedLinkScheme
	}
	return LinkScheme
}

// DefaultInferencePort is the inference port of a new install.
func (i Instance) DefaultInferencePort() uint16 {
	if i.Isolated {
		return isolatedDefaultInferencePort
	}
	return defaultInferencePort
}

// AppGroup is the macOS App Group the instance shares with its widget.
func (i Instance) AppGroup() string { return teamID + "." + i.Identifier() }

// InitializationScript tells pages which instance they run in.
func (i Instance) InitializationScript() string {
	return "window.__ASTRLINK_ISOLATED__ = " + strconv.FormatBool(i.Isolated) + ";"
}

// Directories are where an instance keeps its files, by the rules Tauri
// named them with, so an upgrade from the Tauri shell finds them.
type Directories struct {
	// Data holds Core's database, local key and backups.
	Data string
	// Config holds desktop-preferences.json, the raw key pins and the
	// exchange rates. It is Data but on Linux.
	Config string
}

// Dirs returns the instance's directories for this platform and user.
func (i Instance) Dirs() (Directories, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Directories{}, fmt.Errorf("unable to resolve the home directory: %w", err)
	}
	return dirsFor(runtime.GOOS, home, os.Getenv, i.Identifier())
}

// dirsFor is Tauri's app_data_dir and app_config_dir: the platform's data
// and config directories joined with the identifier.
func dirsFor(goos, home string, getenv func(string) string, identifier string) (Directories, error) {
	switch goos {
	case "darwin":
		dir := filepath.Join(home, "Library", "Application Support", identifier)
		return Directories{Data: dir, Config: dir}, nil
	case "windows":
		appData := getenv("APPDATA")
		if appData == "" {
			return Directories{}, errors.New("unable to resolve %APPDATA%")
		}
		dir := filepath.Join(appData, identifier)
		return Directories{Data: dir, Config: dir}, nil
	}
	data := getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	config := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	return Directories{Data: filepath.Join(data, identifier), Config: filepath.Join(config, identifier)}, nil
}

// EveryInstanceDirectory lists, for one of the instance's directories, that
// directory of every instance, in the same order whichever one asks: their
// directories are named after their identifiers and sit side by side. A
// directory named otherwise stands alone.
func EveryInstanceDirectory(directory string) []string {
	identifiers := []string{Identifier, IsolatedIdentifier}
	parent, name := filepath.Dir(directory), filepath.Base(directory)
	if parent == directory || !slices.Contains(identifiers, name) {
		return []string{directory}
	}
	every := make([]string, len(identifiers))
	for i, identifier := range identifiers {
		every[i] = filepath.Join(parent, identifier)
	}
	return every
}
