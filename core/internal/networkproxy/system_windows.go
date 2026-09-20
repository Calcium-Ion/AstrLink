package networkproxy

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var winHTTP = windows.NewLazySystemDLL("winhttp.dll")
var getUserProxyConfig = winHTTP.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var globalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

// WINHTTP_CURRENT_USER_IE_PROXY_CONFIG. BOOL is 32 bits; Go supplies
// the native padding before the pointers on both 32-bit and 64-bit Windows.
type userProxyConfig struct {
	autoDetect         uint32
	pac, proxy, bypass *uint16
}

func systemSettings() (settings, error) {
	if err := getUserProxyConfig.Find(); err != nil {
		return settings{}, err
	}
	var native userProxyConfig
	ok, _, err := getUserProxyConfig.Call(uintptr(unsafe.Pointer(&native)))
	if ok == 0 {
		return settings{}, fmt.Errorf("Windows proxy query failed: %w", err)
	}
	// Windows allocates each string using GlobalAlloc. Copy before freeing.
	defer freeSystemString(native.pac)
	defer freeSystemString(native.proxy)
	defer freeSystemString(native.bypass)
	server := windows.UTF16PtrToString(native.proxy)
	config, err := parseWindowsSettings(server != "", server, windows.UTF16PtrToString(native.bypass))
	config.automatic = native.autoDetect != 0 || windows.UTF16PtrToString(native.pac) != ""
	return config, err
}

func freeSystemString(value *uint16) {
	if value != nil {
		globalFree.Call(uintptr(unsafe.Pointer(value)))
	}
}
