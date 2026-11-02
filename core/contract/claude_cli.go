package contract

// ClaudeCLIStatus describes the user's claude CLI, which official Claude
// subscription accounts run.
type ClaudeCLIStatus struct {
	// Installed is true when an executable was found, even a too-old one.
	Installed bool `json:"installed"`
	// Supported is true when the found executable is new enough.
	Supported      bool   `json:"supported"`
	Path           string `json:"path,omitempty"`
	Version        string `json:"version,omitempty"`
	MinimumVersion string `json:"minimum_version"`
	// InstallAvailable is true where Core installs the CLI on request (the
	// server edition); the desktop links to the official instructions.
	InstallAvailable bool   `json:"install_available"`
	Installing       bool   `json:"installing"`
	InstallError     string `json:"install_error,omitempty"`
}
