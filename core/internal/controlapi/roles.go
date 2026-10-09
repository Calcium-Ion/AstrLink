package controlapi

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Role is the privilege a control caller holds. Roles are ordered: a caller
// satisfies any requirement at or below its own role.
type Role uint8

const (
	// RoleNone is an unauthenticated caller.
	RoleNone Role = iota
	// RoleObserver reads records and settings. The same-uid control socket and
	// the observer token published for agent tools resolve to it.
	RoleObserver
	// RoleOperator is the desktop shell holding the per-start control token.
	RoleOperator
)

func (role Role) String() string {
	switch role {
	case RoleObserver:
		return "observer"
	case RoleOperator:
		return "operator"
	default:
		return "none"
	}
}

// roleFromRequest resolves the caller's role. A console session and the
// operator token are checked first so an operator is never downgraded; the
// observer token and the local socket both resolve to observer.
func (handler *Handler) roleFromRequest(request *http.Request) Role {
	if consoleSessionAuthenticated(request) {
		return RoleOperator
	}
	const prefix = "Bearer "
	if authorization := request.Header.Get("Authorization"); strings.HasPrefix(authorization, prefix) {
		provided := []byte(strings.TrimPrefix(authorization, prefix))
		if tokenMatches(provided, handler.controlToken) {
			return RoleOperator
		}
		if tokenMatches(provided, handler.observerToken) {
			return RoleObserver
		}
	}
	if LocalSocketAuthenticated(request) {
		return RoleObserver
	}
	return RoleNone
}

func tokenMatches(provided, expected []byte) bool {
	return len(expected) > 0 && len(provided) == len(expected) &&
		subtle.ConstantTimeCompare(provided, expected) == 1
}

func isSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// authenticated admits reads (GET, HEAD) from callers holding at least
// minRole and every other method only from operators, so a route that forgets
// its mark never opens a write to observers. Routes whose subpaths differ use
// authenticatedBy.
func (handler *Handler) authenticated(next http.HandlerFunc, minRole Role) http.HandlerFunc {
	return handler.authenticatedBy(next, func(request *http.Request) Role {
		if isSafeMethod(request.Method) {
			return minRole
		}
		return RoleOperator
	})
}

// authenticatedBy resolves the required role per request. It is for routes
// that multiplex a sensitive read or an observer-safe write under one
// registration; required must still return operator for anything it does not
// explicitly relax.
func (handler *Handler) authenticatedBy(next http.HandlerFunc, required func(*http.Request) Role) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		role := handler.roleFromRequest(request)
		if role == RoleNone {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-control"`)
			writeError(
				writer,
				http.StatusUnauthorized,
				"unauthorized",
				"missing or invalid local control token",
			)
			return
		}
		// Authenticated agent-side readers are recorded before dispatch so the
		// desktop can show that records are being read while the call runs.
		if request.URL.Path != ObserversPath {
			handler.observers.note(request)
		}
		minimum := required(request)
		if minimum < RoleObserver {
			minimum = RoleObserver
		}
		if role < minimum {
			writeError(
				writer,
				http.StatusForbidden,
				"forbidden",
				"this operation requires the "+minimum.String()+" role",
			)
			return
		}
		next(writer, request.WithContext(contextWithRole(request.Context(), role)))
	}
}

type roleContextKey struct{}

func contextWithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleContextKey{}, role)
}

// requestRole returns the role authenticatedBy resolved for this request.
func requestRole(request *http.Request) Role {
	role, _ := request.Context().Value(roleContextKey{}).(Role)
	return role
}
