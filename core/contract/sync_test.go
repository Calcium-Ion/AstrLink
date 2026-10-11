package contract

import (
	"strings"
	"testing"
)

func TestDefaultSyncConfigCarriesAFolderAndPathAddressing(t *testing.T) {
	config := DefaultSyncConfig()
	if config.Remote.Path != SyncDefaultFolder {
		t.Fatalf("folder = %q", config.Remote.Path)
	}
	if config.Remote.Addressing != SyncS3AddressingPath {
		t.Fatalf("addressing = %q", config.Remote.Addressing)
	}
	if err := ValidateSyncConfig(config); err != nil {
		t.Fatalf("the default configuration was refused: %v", err)
	}
}

// A WebDAV remote without a folder would write the package at the server root,
// where it is easily lost and easily overwritten.
func TestValidateSyncConfigRequiresAWebDAVFolder(t *testing.T) {
	config := DefaultSyncConfig()
	config.Remote = SyncRemote{Kind: SyncRemoteWebDAV, Endpoint: "https://dav.example"}
	if err := ValidateSyncConfig(config); err == nil {
		t.Fatal("a WebDAV remote without a folder was accepted")
	}
	config.Remote.Path = "  "
	if err := ValidateSyncConfig(config); err == nil {
		t.Fatal("a blank folder was accepted")
	}
	config.Remote.Path = SyncDefaultFolder
	if err := ValidateSyncConfig(config); err != nil {
		t.Fatalf("a complete WebDAV remote was refused: %v", err)
	}
}

func TestValidateSyncConfigRefusesAnUnknownS3Addressing(t *testing.T) {
	config := DefaultSyncConfig()
	config.Remote = SyncRemote{
		Kind: SyncRemoteS3, Endpoint: "https://s3.example", Path: "bucket", Addressing: "host",
	}
	if err := ValidateSyncConfig(config); err == nil {
		t.Fatal("an unknown addressing was accepted")
	}
	for _, addressing := range []string{SyncS3AddressingPath, SyncS3AddressingSuffix, ""} {
		config.Remote.Addressing = addressing
		if err := ValidateSyncConfig(config); err != nil {
			t.Fatalf("addressing %q was refused: %v", addressing, err)
		}
	}
}

func TestValidateSyncConfigBoundsTheDeviceName(t *testing.T) {
	config := DefaultSyncConfig()
	config.DeviceName = strings.Repeat("x", MaxSyncDeviceNameLength)
	if err := ValidateSyncConfig(config); err != nil {
		t.Fatalf("a name at the bound was refused: %v", err)
	}
	config.DeviceName = strings.Repeat("x", MaxSyncDeviceNameLength+1)
	if err := ValidateSyncConfig(config); err == nil {
		t.Fatal("an over-long device name was accepted")
	}
	// A name is counted in characters, not bytes, so a multi-byte name is not
	// cut short of the bound.
	config.DeviceName = strings.Repeat("电", MaxSyncDeviceNameLength)
	if err := ValidateSyncConfig(config); err != nil {
		t.Fatalf("a multi-byte name at the bound was refused: %v", err)
	}
}
