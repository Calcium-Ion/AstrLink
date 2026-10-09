package controlapi

import (
	"context"
	"net/http"
)

type consoleSessionKey struct{}

// ContextWithConsoleSession marks a request the server edition's web console
// accepted with a signed-in session. The console password is the operator
// credential there, so such a request holds RoleOperator.
func ContextWithConsoleSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, consoleSessionKey{}, true)
}

func consoleSessionAuthenticated(request *http.Request) bool {
	value, _ := request.Context().Value(consoleSessionKey{}).(bool)
	return value
}

// ConsoleSessions are the server edition's signed-in browser sessions. The
// raw password is also the console password there, so the raw password
// endpoint allows only change: set is the console's first-run setup, and a
// reset needs no old password, which would let a stolen session take the
// console over. A change ends every other session.
type ConsoleSessions interface {
	// EndOtherSessions ends every console session except the request's own.
	EndOtherSessions(*http.Request)
}

// WriteError writes the control API error envelope with its JSON headers, so
// a browser parses one error shape on every console path.
func WriteError(writer http.ResponseWriter, status int, code, message string) {
	setHeaders(writer.Header())
	writeError(writer, status, code, message)
}

// DecodePasswordJSON reads a {"password": "..."} body the way the raw
// password endpoints do, so the password arrives as bytes the caller clears
// and no other copy outlives the request. It answers the request itself and
// reports false when the body is not that shape.
func DecodePasswordJSON(writer http.ResponseWriter, request *http.Request) ([]byte, bool) {
	setHeaders(writer.Header())
	members, release, ok := decodeSecretJSON(writer, request, "password")
	if !ok {
		return nil, false
	}
	defer release()
	password, err := decodeJSONStringBytes(members["password"])
	if err != nil || len(password) == 0 {
		clear(password)
		writeValidationFailed(writer, "password is invalid", []errorDetail{{
			Field: "password", Reason: "must be a non-empty string",
		}})
		return nil, false
	}
	return password, true
}
