package taskplugin

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// serviceAccount is the part of a Google service account key the token
// exchange reads: the provider's key holds the whole JSON.
type serviceAccount struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

const (
	defaultTokenURI = "https://oauth2.googleapis.com/token"
	cloudScope      = "https://www.googleapis.com/auth/cloud-platform"
	// tokenMargin renews a token this long before it expires.
	tokenMargin = 5 * time.Minute
)

type cachedToken struct {
	token   string
	expires time.Time
	account *serviceAccount
	// key is the key the token was made from: a changed key gets a new
	// token.
	key [32]byte
}

// tokenCache keeps the access tokens of oauth2_jwt providers until they
// are about to expire.
type tokenCache struct {
	mutex  sync.Mutex
	tokens map[contract.ServiceID]cachedToken
	// exchange serializes the exchanges of one provider.
	exchange sync.Map
}

func newTokenCache() *tokenCache {
	return &tokenCache{tokens: map[contract.ServiceID]cachedToken{}}
}

// token returns an access token for the service account key holds, from
// the cache while it is valid. The account is returned when key parses,
// for ctx.auth to name its project even when the exchange fails.
func (cache *tokenCache) token(ctx context.Context, engine *Engine, service contract.Service, key string) (string, *serviceAccount, error) {
	var account serviceAccount
	if err := json.Unmarshal([]byte(key), &account); err != nil || account.PrivateKey == "" || account.ClientEmail == "" {
		return "", nil, errors.New("the provider's key is not a Google service account key (JSON)")
	}
	sum := sha256.Sum256([]byte(key))
	value, _ := cache.exchange.LoadOrStore(service.ID, &sync.Mutex{})
	exchange := value.(*sync.Mutex)
	exchange.Lock()
	defer exchange.Unlock()
	now := engine.now()
	cache.mutex.Lock()
	cached, ok := cache.tokens[service.ID]
	cache.mutex.Unlock()
	if ok && cached.key == sum && now.Add(tokenMargin).Before(cached.expires) {
		return cached.token, cached.account, nil
	}
	token, lifetime, err := exchangeServiceAccount(ctx, engine, service, account, now)
	if err != nil {
		return "", &account, err
	}
	cache.mutex.Lock()
	cache.tokens[service.ID] = cachedToken{token: token, expires: now.Add(lifetime), account: &account, key: sum}
	cache.mutex.Unlock()
	return token, &account, nil
}

// exchangeServiceAccount trades a signed assertion for an access token at
// the account's token endpoint, through the provider's proxy.
func exchangeServiceAccount(ctx context.Context, engine *Engine, service contract.Service, account serviceAccount, now time.Time) (string, time.Duration, error) {
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return "", 0, errors.New("the service account's private key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if pkcs1, pkcs1Err := x509.ParsePKCS1PrivateKey(block.Bytes); pkcs1Err == nil {
			parsed, err = pkcs1, nil
		}
	}
	private, ok := parsed.(*rsa.PrivateKey)
	if err != nil || !ok {
		return "", 0, errors.New("the service account's private key is not an RSA key")
	}
	tokenURI := account.TokenURI
	if tokenURI == "" {
		tokenURI = defaultTokenURI
	}
	endpoint, err := url.Parse(tokenURI)
	loopback := err == nil && endpoint.Scheme == "http" && isLoopback(endpoint.Hostname())
	if err != nil || (endpoint.Scheme != "https" && !loopback) || endpoint.Host == "" {
		return "", 0, errors.New("the service account's token_uri is not an https URL")
	}
	encode := base64.RawURLEncoding.EncodeToString
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": account.PrivateKeyID})
	claims, _ := json.Marshal(map[string]any{
		"iss": account.ClientEmail, "scope": cloudScope, "aud": tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	unsigned := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, private, crypto.SHA256, digest[:])
	if err != nil {
		return "", 0, errors.New("the service account's key cannot sign")
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {unsigned + "." + encode(signature)}}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, (&url.URL{Path: endpoint.EscapedPath(), RawQuery: endpoint.RawQuery}).String(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	origin := &url.URL{Scheme: endpoint.Scheme, Host: endpoint.Host}
	headers := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
	response, err := engine.send(request, transport.Target{Service: service, ProxyCredentials: engine.proxyCredentials, BaseURL: origin, RequestHeaders: headers})
	if err != nil {
		return "", 0, fmt.Errorf("the token exchange with %s failed", endpoint.Host)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var answer struct {
		AccessToken      string `json:"access_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &answer)
	if response.StatusCode != http.StatusOK || answer.AccessToken == "" {
		reason := answer.ErrorDescription
		if reason == "" {
			reason = answer.Error
		}
		if reason == "" {
			reason = http.StatusText(response.StatusCode)
		}
		return "", 0, fmt.Errorf("the token exchange with %s was refused: %s", endpoint.Host, reason)
	}
	lifetime := time.Duration(answer.ExpiresIn) * time.Second
	if lifetime <= tokenMargin {
		lifetime = time.Hour
	}
	return answer.AccessToken, lifetime, nil
}

// isLoopback reports whether host is this machine, the one place a token
// endpoint may be plain http.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
