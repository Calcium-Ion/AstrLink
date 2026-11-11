package endpoint

import (
	"context"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
)

type accessTokenServicesKey struct{}

// WithAccessTokenServices limits every provider the resolver returns for ctx
// to the ones the presenting access token may use.
func WithAccessTokenServices(ctx context.Context, services contract.AccessTokenServices) context.Context {
	return context.WithValue(ctx, accessTokenServicesKey{}, services)
}

// AccessTokenServicesFrom returns the provider scope of the request's access
// token. A request without one may use every provider.
func AccessTokenServicesFrom(ctx context.Context) contract.AccessTokenServices {
	services, _ := ctx.Value(accessTokenServicesKey{}).(contract.AccessTokenServices)
	return services
}

// AccessTokenScopeError reports that the request's access token may not use
// any provider that could serve it. Service is the provider the client named,
// when it named one outside the scope. The message never lists providers the
// token cannot see.
type AccessTokenScopeError struct {
	Service contract.ServiceID
	Model   string
}

func (err *AccessTokenScopeError) Error() string {
	if err.Service != "" {
		return fmt.Sprintf("access token may not use API provider %q", err.Service)
	}
	if err.Model != "" {
		return fmt.Sprintf("none of the API providers this access token may use can serve model %q", err.Model)
	}
	return "none of the API providers this access token may use can serve this request"
}

// limitToAccessTokenScope splits services into the ones the token may use and
// the ones it may not, and ranks the latter as skipped for that reason.
func limitToAccessTokenScope(services []contract.Service, ranking []RankedService, scope contract.AccessTokenServices) (kept, dropped []contract.Service) {
	if !scope.Restricted() {
		return services, nil
	}
	for _, service := range services {
		if scope.Allows(service.ID) {
			kept = append(kept, service)
		} else {
			dropped = append(dropped, service)
		}
	}
	for index := range ranking {
		if !scope.Allows(ranking[index].ServiceID) {
			ranking[index].Skip = contract.RoutingSkipAccessTokenScope
		}
	}
	return kept, dropped
}
