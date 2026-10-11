package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// maxPackageBytes bounds what the sync reads from a remote, so a hostile or
// broken store cannot exhaust memory.
const maxPackageBytes = 64 << 20

// Config names one remote store and the credentials for it.
type Config struct {
	Kind      string
	Endpoint  string
	Path      string
	Region    string
	Username  string
	Password  string
	AccessKey string
	SecretKey string
	// Addressing selects how an S3 request names its bucket: "path" keeps it in
	// the URL path, "suffix" builds it into the host from the endpoint, which is
	// then read as a domain suffix. Empty is the path style.
	Addressing string
}

// packageName and lockName are the objects under the configured path.
const (
	packageName = "astrlink-config.enc"
	lockName    = "astrlink-config.lock"
)

// New builds the remote a configuration names. It returns ErrUnsupported for a
// kind this build does not implement.
func New(config Config, client *http.Client, now func() time.Time) (Remote, error) {
	if now == nil {
		now = time.Now
	}
	switch config.Kind {
	case "webdav":
		return newWebDAV(config, client, now)
	case "s3":
		return newS3(config, client, now)
	default:
		return nil, fmt.Errorf("%w: remote kind %q", ErrUnsupported, config.Kind)
	}
}

// storeIdentity names a store by its kind and location, so a package written
// to one location is refused when the configuration points at another. It
// carries no secret.
func storeIdentity(kind, endpoint, bucket string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + endpoint + "\x00" + bucket))
	return kind + ":" + hex.EncodeToString(sum[:8])
}

func encodeLock(lock Lock) ([]byte, error) {
	return json.Marshal(lock)
}

func decodeLock(payload []byte) (Lock, error) {
	var lock Lock
	if err := json.Unmarshal(payload, &lock); err != nil {
		return Lock{}, fmt.Errorf("remote lock is not readable: %w", err)
	}
	if lock.Owner == "" || lock.Nonce == "" {
		return Lock{}, fmt.Errorf("remote lock is incomplete")
	}
	return lock, nil
}

// webdav stores the package as two files on a WebDAV server.
type webdav struct {
	base     string
	username string
	password string
	client   *http.Client
	now      func() time.Time
	identity string
}

func newWebDAV(config Config, client *http.Client, now func() time.Time) (*webdav, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("a WebDAV remote needs an endpoint")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	base, err := url.Parse(config.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse WebDAV endpoint: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("WebDAV endpoint must be an absolute http(s) URL")
	}
	if base.User != nil {
		return nil, fmt.Errorf("WebDAV endpoint must not contain credentials")
	}
	base.Path = path.Join(base.Path, config.Path)
	return &webdav{
		base:     strings.TrimSuffix(base.String(), "/"),
		username: config.Username,
		password: config.Password,
		client:   client,
		now:      now,
		identity: storeIdentity("webdav", base.String(), ""),
	}, nil
}

func (dav *webdav) Identity(context.Context) (string, error) { return dav.identity, nil }

func (dav *webdav) Describe() string { return dav.base }

func (dav *webdav) object(name string) string { return dav.base + "/" + name }

func (dav *webdav) do(ctx context.Context, method, target string, body []byte, conditions map[string]string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if dav.username != "" || dav.password != "" {
		request.SetBasicAuth(dav.username, dav.password)
	}
	for header, value := range conditions {
		request.Header.Set(header, value)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	response, err := dav.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return response, nil
}

// check maps a WebDAV status onto the package's errors.
func check(response *http.Response) error {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusPreconditionFailed, response.StatusCode == http.StatusConflict:
		return ErrPrecondition
	case response.StatusCode == http.StatusUnauthorized, response.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case response.StatusCode == http.StatusMethodNotAllowed, response.StatusCode == http.StatusNotImplemented:
		return ErrUnsupported
	default:
		return fmt.Errorf("%w: HTTP %d", ErrUnreachable, response.StatusCode)
	}
}

func readBody(response *http.Response, limit int64) ([]byte, error) {
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return payload, nil
}

func (dav *webdav) Read(ctx context.Context) ([]byte, string, bool, error) {
	response, err := dav.do(ctx, http.MethodGet, dav.object(packageName), nil, nil)
	if err != nil {
		return nil, "", false, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return nil, "", false, nil
	}
	if err := check(response); err != nil {
		response.Body.Close()
		return nil, "", false, err
	}
	payload, err := readBody(response, maxPackageBytes)
	if err != nil {
		return nil, "", false, err
	}
	return payload, response.Header.Get("ETag"), true, nil
}

func (dav *webdav) Write(ctx context.Context, payload []byte, expectedETag string) (string, error) {
	conditions := map[string]string{}
	if expectedETag == "" {
		conditions["If-None-Match"] = "*"
	} else {
		conditions["If-Match"] = expectedETag
	}
	response, err := dav.do(ctx, http.MethodPut, dav.object(packageName), payload, conditions)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if err := check(response); err != nil {
		return "", err
	}
	return response.Header.Get("ETag"), nil
}

func (dav *webdav) ReadLock(ctx context.Context) (Lock, string, bool, error) {
	response, err := dav.do(ctx, http.MethodGet, dav.object(lockName), nil, nil)
	if err != nil {
		return Lock{}, "", false, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return Lock{}, "", false, nil
	}
	if err := check(response); err != nil {
		response.Body.Close()
		return Lock{}, "", false, err
	}
	payload, err := readBody(response, 4096)
	if err != nil {
		return Lock{}, "", false, err
	}
	lock, err := decodeLock(payload)
	if err != nil {
		return Lock{}, "", false, err
	}
	return lock, response.Header.Get("ETag"), true, nil
}

func (dav *webdav) WriteLock(ctx context.Context, lock Lock, expectedETag string) (string, error) {
	payload, err := encodeLock(lock)
	if err != nil {
		return "", err
	}
	conditions := map[string]string{}
	if expectedETag == "" {
		conditions["If-None-Match"] = "*"
	} else {
		conditions["If-Match"] = expectedETag
	}
	response, err := dav.do(ctx, http.MethodPut, dav.object(lockName), payload, conditions)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if err := check(response); err != nil {
		return "", err
	}
	return response.Header.Get("ETag"), nil
}

func (dav *webdav) DeleteLock(ctx context.Context, expectedETag string) error {
	conditions := map[string]string{}
	if expectedETag != "" {
		conditions["If-Match"] = expectedETag
	}
	response, err := dav.do(ctx, http.MethodDelete, dav.object(lockName), nil, conditions)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	return check(response)
}
