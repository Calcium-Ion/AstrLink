//go:build darwin

package keystore

import (
	"os"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
)

// KeychainEnabled reports whether the local key lives in the keychain:
// signed builds running from their bundle keep it there. Development builds
// and ad hoc signatures keep the key file, so a rebuild never raises a
// keychain prompt.
func KeychainEnabled() bool {
	if mygo.IsDev() {
		return false
	}
	executable, err := os.Executable()
	if err != nil || !strings.Contains(executable, ".app/Contents/MacOS/") {
		return false
	}
	// An ad hoc signature has no certificate chain to anchor.
	return signedWith("anchor apple generic")
}

var security struct {
	once sync.Once
	ok   bool

	cfStringCreate    func(allocator uintptr, text string, encoding uint32) uintptr
	cfRelease         func(ref uintptr)
	requirementCreate func(text uintptr, flags uint32, requirement *uintptr) int32
	codeCopySelf      func(flags uint32, code *uintptr) int32
	codeCheckValidity func(code uintptr, flags uint32, requirement uintptr) int32
}

func loadSecurity() bool {
	security.once.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		purego.RegisterLibFunc(&security.cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&security.cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&security.requirementCreate, sec, "SecRequirementCreateWithString")
		purego.RegisterLibFunc(&security.codeCopySelf, sec, "SecCodeCopySelf")
		purego.RegisterLibFunc(&security.codeCheckValidity, sec, "SecCodeCheckValidity")
		security.ok = true
	})
	return security.ok
}

// signedWith reports whether this process's code signature is valid and
// satisfies requirement (SecCodeCheckValidity).
func signedWith(requirement string) bool {
	if !loadSecurity() {
		return false
	}
	const utf8 = 0x08000100 // kCFStringEncodingUTF8
	text := security.cfStringCreate(0, requirement, utf8)
	if text == 0 {
		return false
	}
	defer security.cfRelease(text)
	var req, code uintptr
	if security.requirementCreate(text, 0, &req) != 0 || req == 0 {
		return false
	}
	defer security.cfRelease(req)
	if security.codeCopySelf(0, &code) != 0 || code == 0 {
		return false
	}
	defer security.cfRelease(code)
	return security.codeCheckValidity(code, 0, req) == 0
}
