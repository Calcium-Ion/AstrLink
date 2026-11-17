package raw

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestLogindLocksOnlyForThisSession(t *testing.T) {
	const own = dbus.ObjectPath("/org/freedesktop/login1/session/_32")
	changed := func(iface string, values map[string]dbus.Variant) *dbus.Signal {
		return &dbus.Signal{Path: own, Name: propertiesChanged, Body: []any{iface, values, []string{}}}
	}
	for name, c := range map[string]struct {
		signal *dbus.Signal
		locked bool
	}{
		"lock":            {&dbus.Signal{Path: own, Name: logindSession + ".Lock"}, true},
		"another session": {&dbus.Signal{Path: "/org/freedesktop/login1/session/c1", Name: logindSession + ".Lock"}, false},
		"unlock":          {&dbus.Signal{Path: own, Name: logindSession + ".Unlock"}, false},
		"locked hint":     {changed(logindSession, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(true)}), true},
		"unlocked hint":   {changed(logindSession, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(false)}), false},
		"other property":  {changed(logindSession, map[string]dbus.Variant{"IdleHint": dbus.MakeVariant(true)}), false},
		"other interface": {changed("org.freedesktop.login1.User", map[string]dbus.Variant{"LockedHint": dbus.MakeVariant(true)}), false},
		"malformed hint":  {changed(logindSession, map[string]dbus.Variant{"LockedHint": dbus.MakeVariant("yes")}), false},
		"truncated body":  {&dbus.Signal{Path: own, Name: propertiesChanged, Body: []any{logindSession}}, false},
		"no signal":       {nil, false},
		"PrepareForSleep": {&dbus.Signal{Path: logindPath, Name: logindManager + ".PrepareForSleep", Body: []any{true}}, false},
	} {
		if got := sessionLocked(c.signal, own); got != c.locked {
			t.Fatalf("%s: locked = %v", name, got)
		}
	}
}
