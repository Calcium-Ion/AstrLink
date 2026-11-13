package raw

import (
	"context"
	"log"
	"os"

	"github.com/godbus/dbus/v5"
)

const (
	logind            = "org.freedesktop.login1"
	logindPath        = dbus.ObjectPath("/org/freedesktop/login1")
	logindManager     = "org.freedesktop.login1.Manager"
	logindSession     = "org.freedesktop.login1.Session"
	propertiesChanged = "org.freedesktop.DBus.Properties.PropertiesChanged"
)

// watchSession follows logind on the system bus: the Lock signal of this
// process's session, which loginctl lock-session and display managers send,
// and its LockedHint, which screen lockers set.
func watchSession(ctx context.Context, lock func(reason string)) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Printf("astrlink: logind is unavailable, so raw content locks on the screen saver and after idle time only: %v", err)
		return
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	session, ok := ownSession(conn)
	if !ok {
		return
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender(logind),
		dbus.WithMatchObjectPath(session),
		dbus.WithMatchInterface(logindSession),
		dbus.WithMatchMember("Lock"),
	); err != nil {
		log.Printf("astrlink: unable to follow this logind session's lock: %v", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchSender(logind),
		dbus.WithMatchObjectPath(session),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchArg(0, logindSession),
	); err != nil {
		log.Printf("astrlink: unable to follow this logind session's locked hint: %v", err)
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	go func() {
		for signal := range signals {
			if sessionLocked(signal, session) {
				lock("screen lock")
			}
		}
	}()
}

// ownSession is the logind session this process runs in.
func ownSession(conn *dbus.Conn) (dbus.ObjectPath, bool) {
	var session dbus.ObjectPath
	err := conn.Object(logind, logindPath).
		Call(logindManager+".GetSessionByPID", 0, uint32(os.Getpid())).Store(&session)
	if err != nil {
		log.Printf("astrlink: unable to find this logind session: %v", err)
		return "", false
	}
	return session, true
}

// sessionLocked reports whether signal says session locked: its Lock
// signal, or LockedHint turning true.
func sessionLocked(signal *dbus.Signal, session dbus.ObjectPath) bool {
	if signal == nil || signal.Path != session {
		return false
	}
	switch signal.Name {
	case logindSession + ".Lock":
		return true
	case propertiesChanged:
		if len(signal.Body) < 2 {
			return false
		}
		if iface, _ := signal.Body[0].(string); iface != logindSession {
			return false
		}
		changed, _ := signal.Body[1].(map[string]dbus.Variant)
		hint, ok := changed["LockedHint"]
		if !ok {
			return false
		}
		locked, _ := hint.Value().(bool)
		return locked
	}
	return false
}
