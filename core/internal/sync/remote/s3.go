package remote

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// s3 stores the package in an S3-compatible bucket, using AWS Signature
// Version 4 and the conditional-write headers the sync needs.
//
// A bucket is addressed either in the URL path or in the host name. The path
// style is served by every S3-compatible store; the suffix style is for a store
// that expects the virtual-host form, where the endpoint is only the base
// domain and the host is built from the bucket, the region, and that domain.
//
// Conditional writes (If-Match, If-None-Match) are what make the remote lock
// safe, but support varies across S3-compatible stores. The first conditional
// write that comes back Not Implemented is remembered, and every later call
// refuses with ErrUnsupported instead of degrading to an unconditional write,
// which would let two devices overwrite each other.
type s3 struct {
	endpoint    string
	bucket      string
	prefix      string
	region      string
	addressing  string
	accessKey   string
	secretKey   string
	client      *http.Client
	now         func() time.Time
	identity    string
	conditional bool // false once the store refused a conditional write
}

const s3SigningTime = "20060102T150405Z"
const s3ShortDate = "20060102"

func newS3(config Config, client *http.Client, now func() time.Time) (*s3, error) {
	if strings.TrimSpace(config.Endpoint) == "" {
		return nil, fmt.Errorf("an S3 remote needs an endpoint")
	}
	if strings.TrimSpace(config.Path) == "" {
		return nil, fmt.Errorf("an S3 remote needs a bucket")
	}
	if config.AccessKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("an S3 remote needs an access key and secret")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	region := config.Region
	if region == "" {
		region = "us-east-1"
	}
	addressing := config.Addressing
	if addressing == "" {
		addressing = "path"
	}
	endpoint, prefix, err := s3Endpoint(config, addressing)
	if err != nil {
		return nil, err
	}
	return &s3{
		endpoint:    endpoint,
		bucket:      config.Path,
		prefix:      prefix,
		region:      region,
		addressing:  addressing,
		accessKey:   config.AccessKey,
		secretKey:   config.SecretKey,
		client:      client,
		now:         now,
		identity:    storeIdentity("s3", config.Endpoint, config.Path),
		conditional: true,
	}, nil
}

// s3Endpoint resolves where an S3 request goes.
//
// The path style keeps whatever path the endpoint carried, so a gateway that
// serves many buckets under one host still works. The suffix style treats the
// endpoint as a base domain, with no path of its own, and builds the host from
// the bucket, the fixed "s3" service label, and that domain, as in
// <bucket>.s3.<domain>. The region is not part of the host: an S3-compatible
// store that needs the virtual-host form serves its buckets under its own
// domain, while the region still selects the signing scope.
func s3Endpoint(config Config, addressing string) (string, string, error) {
	raw := config.Endpoint
	if addressing == "suffix" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	endpoint, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("parse S3 endpoint: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return "", "", fmt.Errorf("S3 endpoint must be an absolute http(s) URL")
	}
	if endpoint.Host == "" {
		return "", "", fmt.Errorf("S3 endpoint needs a host")
	}
	switch addressing {
	case "suffix":
		if strings.Trim(endpoint.Path, "/") != "" {
			return "", "", fmt.Errorf("an S3 suffix endpoint is a domain, without a path")
		}
		return endpoint.Scheme + "://" + config.Path + ".s3." + endpoint.Host, "", nil
	default:
		return endpoint.Scheme + "://" + endpoint.Host, strings.Trim(endpoint.Path, "/"), nil
	}
}

func (store *s3) Identity(context.Context) (string, error) { return store.identity, nil }

func (store *s3) Describe() string { return "s3://" + store.bucket }

// objectURL builds the URL for one object. The path style keeps the bucket in
// the path, which every S3-compatible store serves; the suffix style already
// carries it in the host.
func (store *s3) objectURL(name string) string {
	parts := []string{}
	if store.addressing != "suffix" {
		parts = append(parts, store.bucket)
	}
	if store.prefix != "" {
		parts = append(parts, store.prefix)
	}
	parts = append(parts, name)
	return store.endpoint + "/" + strings.Join(parts, "/")
}

// do signs and sends one request. The body is always given as bytes, so it can
// be hashed for the signature and sent unchanged.
func (store *s3) do(ctx context.Context, method, name string, body []byte, conditions map[string]string) (*http.Response, error) {
	target := store.objectURL(name)
	parsed, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	now := store.now().UTC()
	payloadHash := sha256.Sum256(body)
	payloadHex := hex.EncodeToString(payloadHash[:])

	headers := map[string]string{
		"host":                 parsed.Host,
		"x-amz-content-sha256": payloadHex,
		"x-amz-date":           now.Format(s3SigningTime),
	}
	for header, value := range conditions {
		headers[strings.ToLower(header)] = value
	}
	signature := store.sign(method, parsed, headers, payloadHex, now)

	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for header, value := range headers {
		if header == "host" {
			continue
		}
		// Restore the canonical casing the server expects.
		request.Header.Set(headerName(header), value)
	}
	request.Header.Set("Authorization", signature)
	if body != nil {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	response, err := store.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	return response, nil
}

// sign computes the AWS Signature Version 4 authorization header.
func (store *s3) sign(method string, parsed *url.URL, headers map[string]string, payloadHex string, now time.Time) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headers[name]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{
		method,
		canonicalURI(parsed),
		parsed.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHex,
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	scope := now.Format(s3ShortDate) + "/" + store.region + "/s3/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format(s3SigningTime),
		scope,
		hex.EncodeToString(requestHash[:]),
	}, "\n")
	signingKey := store.signingKey(now)
	keyHash := hmacSHA256(signingKey, stringToSign)
	return fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		store.accessKey, scope, signedHeaders, hex.EncodeToString(keyHash))
}

func (store *s3) signingKey(now time.Time) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+store.secretKey), now.Format(s3ShortDate))
	regionKey := hmacSHA256(dateKey, store.region)
	serviceKey := hmacSHA256(regionKey, "s3")
	return hmacSHA256(serviceKey, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// canonicalURI encodes the path segments the way the signature expects.
func canonicalURI(parsed *url.URL) string {
	segments := strings.Split(parsed.EscapedPath(), "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			continue
		}
		segments[index] = strings.ReplaceAll(url.PathEscape(decoded), "+", "%20")
	}
	return path.Join(segments...)
}

// headerName restores the conventional casing of a lowercased header.
func headerName(lower string) string {
	parts := strings.Split(lower, "-")
	for index, part := range parts {
		if part == "" {
			continue
		}
		parts[index] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "-")
}

// check maps an S3 status onto the package's errors. Not Implemented on a
// conditional write turns the feature off, so a later call does not silently
// write unconditionally.
func (store *s3) check(response *http.Response, conditional bool) error {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	case response.StatusCode == http.StatusPreconditionFailed:
		return ErrPrecondition
	case response.StatusCode == http.StatusUnauthorized, response.StatusCode == http.StatusForbidden:
		return ErrUnauthorized
	case response.StatusCode == http.StatusNotImplemented:
		if conditional {
			store.conditional = false
			return fmt.Errorf("%w: this S3 store refused a conditional write, so a safe single-writer lock cannot be held", ErrUnsupported)
		}
		return fmt.Errorf("%w: HTTP %d", ErrUnreachable, response.StatusCode)
	default:
		return fmt.Errorf("%w: HTTP %d", ErrUnreachable, response.StatusCode)
	}
}

func (store *s3) requireConditional() error {
	if !store.conditional {
		return fmt.Errorf("%w: this S3 store refused a conditional write earlier in this session", ErrUnsupported)
	}
	return nil
}

func (store *s3) Read(ctx context.Context) ([]byte, string, bool, error) {
	response, err := store.do(ctx, http.MethodGet, packageName, nil, nil)
	if err != nil {
		return nil, "", false, err
	}
	if err := store.check(response, false); err != nil {
		response.Body.Close()
		if response.StatusCode == http.StatusNotFound {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	payload, err := readBody(response, maxPackageBytes)
	if err != nil {
		return nil, "", false, err
	}
	return payload, strings.Trim(response.Header.Get("ETag"), `"`), true, nil
}

func (store *s3) Write(ctx context.Context, payload []byte, expectedETag string) (string, error) {
	if err := store.requireConditional(); err != nil {
		return "", err
	}
	conditions := map[string]string{}
	if expectedETag == "" {
		conditions["If-None-Match"] = "*"
	} else {
		conditions["If-Match"] = expectedETag
	}
	response, err := store.do(ctx, http.MethodPut, packageName, payload, conditions)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if err := store.check(response, true); err != nil {
		return "", err
	}
	return strings.Trim(response.Header.Get("ETag"), `"`), nil
}

func (store *s3) ReadLock(ctx context.Context) (Lock, string, bool, error) {
	response, err := store.do(ctx, http.MethodGet, lockName, nil, nil)
	if err != nil {
		return Lock{}, "", false, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return Lock{}, "", false, nil
	}
	if err := store.check(response, false); err != nil {
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
	return lock, strings.Trim(response.Header.Get("ETag"), `"`), true, nil
}

func (store *s3) WriteLock(ctx context.Context, lock Lock, expectedETag string) (string, error) {
	if err := store.requireConditional(); err != nil {
		return "", err
	}
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
	response, err := store.do(ctx, http.MethodPut, lockName, payload, conditions)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if err := store.check(response, true); err != nil {
		return "", err
	}
	return strings.Trim(response.Header.Get("ETag"), `"`), nil
}

func (store *s3) DeleteLock(ctx context.Context, expectedETag string) error {
	if err := store.requireConditional(); err != nil {
		return err
	}
	conditions := map[string]string{}
	if expectedETag != "" {
		conditions["If-Match"] = expectedETag
	}
	response, err := store.do(ctx, http.MethodDelete, lockName, nil, conditions)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil
	}
	return store.check(response, true)
}
