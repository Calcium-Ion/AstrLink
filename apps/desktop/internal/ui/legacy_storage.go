package ui

import (
	"encoding/json"
	"log"
	"os"
	"runtime"

	"github.com/QuantumNous/astrlink/desktop/internal/ui/webstore"
)

// legacyStorageFlag marks a localStorage that was handed the Tauri
// shell's items: they are handed over once, so an item the pages remove
// later stays removed.
const legacyStorageFlag = "astrlink.legacy-storage.v1"

// readLegacyStorage reads the desktop pages' items the Tauri shell's
// webview kept for the app named identifier.
func readLegacyStorage(identifier string) map[string]string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	items, errs := webstore.Legacy(runtime.GOOS, home, os.Getenv, identifier)
	for _, err := range errs {
		log.Printf("astrlink: unable to read the previous release's page storage: %v", err)
	}
	return items
}

// legacyStorageScript is the main window's preload script that puts the
// items in the page's localStorage before its own scripts run, the ones it
// does not have yet, the first time. It is empty without items.
func legacyStorageScript(items map[string]string) string {
	if len(items) == 0 {
		return ""
	}
	// JSON is a JavaScript expression; Go escapes U+2028 and U+2029.
	encoded, err := json.Marshal(items)
	if err != nil {
		return ""
	}
	flag, _ := json.Marshal(legacyStorageFlag)
	return "(function(){try{var s=window.localStorage,f=" + string(flag) +
		";if(s.getItem(f)!==null)return;var e=" + string(encoded) +
		";for(var k in e)if(Object.prototype.hasOwnProperty.call(e,k)&&s.getItem(k)===null)s.setItem(k,e[k]);" +
		"s.setItem(f,\"1\")}catch(_){}})();\n"
}
