package proxycheck

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var dynamicStore struct {
	once sync.Once
	ok   bool

	copyProxies        func(store uintptr) uintptr
	dictionaryGetValue func(dict, key uintptr) uintptr
	stringCreate       func(allocator uintptr, text string, encoding uint32) uintptr
	stringGetCString   func(text uintptr, buffer *byte, size int, encoding uint32) bool
	numberGetValue     func(number uintptr, kind int, value unsafe.Pointer) bool
	getTypeID          func(ref uintptr) uintptr
	stringTypeID       func() uintptr
	numberTypeID       func() uintptr
	release            func(ref uintptr)
}

const cfUTF8 = 0x08000100 // kCFStringEncodingUTF8

func loadDynamicStore() bool {
	dynamicStore.once.Do(func() {
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		sc, err := purego.Dlopen("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		d := &dynamicStore
		purego.RegisterLibFunc(&d.copyProxies, sc, "SCDynamicStoreCopyProxies")
		purego.RegisterLibFunc(&d.dictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&d.stringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&d.stringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&d.numberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&d.getTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&d.stringTypeID, cf, "CFStringGetTypeID")
		purego.RegisterLibFunc(&d.numberTypeID, cf, "CFNumberGetTypeID")
		purego.RegisterLibFunc(&d.release, cf, "CFRelease")
		d.ok = true
	})
	return dynamicStore.ok
}

// withSystem fills the HTTP and HTTPS proxies the environment leaves unset
// from the dynamic store, as hyper-util does; its exception list is not
// read.
func withSystem(s *settings) {
	if (s.http != "" && s.https != "") || !loadDynamicStore() {
		return
	}
	d := &dynamicStore
	proxies := d.copyProxies(0)
	if proxies == 0 {
		return
	}
	defer d.release(proxies)
	value := func(key string) uintptr {
		k := d.stringCreate(0, key, cfUTF8)
		if k == 0 {
			return 0
		}
		defer d.release(k)
		return d.dictionaryGetValue(proxies, k)
	}
	number := func(key string) (int32, bool) {
		ref := value(key)
		var n int32
		if ref == 0 || d.getTypeID(ref) != d.numberTypeID() || !d.numberGetValue(ref, 3, unsafe.Pointer(&n)) { // kCFNumberSInt32Type
			return 0, false
		}
		return n, true
	}
	text := func(key string) (string, bool) {
		ref := value(key)
		if ref == 0 || d.getTypeID(ref) != d.stringTypeID() {
			return "", false
		}
		buffer := make([]byte, 1024)
		if !d.stringGetCString(ref, &buffer[0], len(buffer), cfUTF8) {
			return "", false
		}
		for i, c := range buffer {
			if c == 0 {
				return string(buffer[:i]), true
			}
		}
		return "", false
	}
	setting := func(prefix string) string {
		enabled, hasEnabled := number(prefix + "Enable")
		host, hasHost := text(prefix + "Proxy")
		port, hasPort := number(prefix + "Port")
		return macSetting(enabled, hasEnabled, host, hasHost, port, hasPort)
	}
	if s.http == "" {
		s.http = setting("HTTP")
	}
	if s.https == "" {
		s.https = setting("HTTPS")
	}
}
