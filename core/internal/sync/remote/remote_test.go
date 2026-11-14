package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// memoryStore is a WebDAV-shaped store that honours If-Match and If-None-Match
// the way a real one does, so the lease logic can be exercised without a
// server.
type memoryStore struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	payload []byte
	etag    string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{objects: map[string]memoryObject{}}
}

func (store *memoryStore) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	store.mu.Lock()
	defer store.mu.Unlock()
	name := strings.TrimPrefix(request.URL.Path, "/dav/")
	existing, exists := store.objects[name]
	switch request.Method {
	case http.MethodGet:
		if !exists {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("ETag", existing.etag)
		writer.Write(existing.payload)
	case http.MethodPut:
		ifNoneMatch := request.Header.Get("If-None-Match")
		ifMatch := request.Header.Get("If-Match")
		if ifNoneMatch == "*" && exists {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		if ifMatch != "" && (!exists || existing.etag != ifMatch) {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		payload := make([]byte, request.ContentLength)
		request.Body.Read(payload)
		etag := fmt.Sprintf(`"v%d"`, len(existing.payload)+len(payload)+len(name))
		store.objects[name] = memoryObject{payload: payload, etag: etag}
		writer.Header().Set("ETag", etag)
		writer.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		ifMatch := request.Header.Get("If-Match")
		if !exists {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if ifMatch != "" && existing.etag != ifMatch {
			writer.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		delete(store.objects, name)
		writer.WriteHeader(http.StatusNoContent)
	default:
		writer.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func testWebDAV(t *testing.T) (Remote, *memoryStore) {
	t.Helper()
	store := newMemoryStore()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/dav/" {
			request.URL.Path = "/dav"
		}
		store.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	target, err := New(Config{Kind: "webdav", Endpoint: server.URL + "/dav"}, server.Client(), time.Now)
	if err != nil {
		t.Fatalf("new remote: %v", err)
	}
	return target, store
}

func TestWebDAVWriteIsConditional(t *testing.T) {
	ctx := context.Background()
	target, _ := testWebDAV(t)
	if _, _, exists, err := target.Read(ctx); err != nil || exists {
		t.Fatalf("empty read = %v, %v", exists, err)
	}
	etag, err := target.Write(ctx, []byte("first"), "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// A second create must fail: the object is no longer absent.
	if _, err := target.Write(ctx, []byte("second"), ""); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("second create = %v, want ErrPrecondition", err)
	}
	// A write with the wrong tag must fail.
	if _, err := target.Write(ctx, []byte("third"), `"stale"`); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale write = %v, want ErrPrecondition", err)
	}
	payload, gotETag, exists, err := target.Read(ctx)
	if err != nil || !exists || string(payload) != "first" || gotETag != etag {
		t.Fatalf("read = %q %q %v %v", payload, gotETag, exists, err)
	}
	if _, err := target.Write(ctx, []byte("fourth"), etag); err != nil {
		t.Fatalf("matching write: %v", err)
	}
}

func TestLeaseSerializesTwoDevices(t *testing.T) {
	ctx := context.Background()
	target, _ := testWebDAV(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	first, err := Acquire(ctx, target, "device_a", time.Minute, clock)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// A second device is refused while the lease is live.
	if _, err := Acquire(ctx, target, "device_b", time.Minute, clock); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	// The same owner may renew its own lock.
	second, err := Acquire(ctx, target, "device_a", time.Minute, clock)
	if err != nil {
		t.Fatalf("owner re-acquire: %v", err)
	}
	if err := first.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	// The re-acquire replaced the lock, so the first lease no longer owns it.
	if err := second.Release(ctx); err != nil {
		t.Fatalf("release second: %v", err)
	}
	_ = second
}

func TestLeaseExpiresSoACrashedDeviceDoesNotDeadlock(t *testing.T) {
	ctx := context.Background()
	target, _ := testWebDAV(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	if _, err := Acquire(ctx, target, "device_a", time.Minute, clock); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	now = now.Add(2 * time.Minute)
	lease, err := Acquire(ctx, target, "device_b", time.Minute, clock)
	if err != nil {
		t.Fatalf("take an expired lease: %v", err)
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// A device whose lease lapsed must not delete the lock a newer owner took.
func TestLapsedOwnerDoesNotReleaseTheNewLock(t *testing.T) {
	ctx := context.Background()
	target, _ := testWebDAV(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	old, err := Acquire(ctx, target, "device_a", time.Minute, clock)
	if err != nil {
		t.Fatalf("acquire old: %v", err)
	}
	now = now.Add(2 * time.Minute)
	fresh, err := Acquire(ctx, target, "device_b", time.Minute, clock)
	if err != nil {
		t.Fatalf("acquire fresh: %v", err)
	}
	// The lapsed holder's release finds a different nonce and does nothing.
	if err := old.Release(ctx); err != nil {
		t.Fatalf("old release: %v", err)
	}
	if _, err := Acquire(ctx, target, "device_c", time.Minute, clock); !errors.Is(err, ErrLocked) {
		t.Fatalf("a lapsed release freed the new lock: %v", err)
	}
	if err := fresh.Release(ctx); err != nil {
		t.Fatalf("fresh release: %v", err)
	}
	if _, err := Acquire(ctx, target, "device_c", time.Minute, clock); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}

func TestRenewFailsWhenAnotherTookTheLock(t *testing.T) {
	ctx := context.Background()
	target, _ := testWebDAV(t)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lease, err := Acquire(ctx, target, "device_a", time.Minute, clock)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := Acquire(ctx, target, "device_b", time.Minute, clock); err != nil {
		t.Fatalf("take over: %v", err)
	}
	if err := lease.Renew(ctx); !errors.Is(err, ErrLocked) {
		t.Fatalf("renew = %v, want ErrLocked", err)
	}
}

// The configured remote identity is stable, so a package written elsewhere is
// refused and remote_etag alone is never trusted.
func TestIdentityIsStableAndLocationBound(t *testing.T) {
	now := time.Now
	first, err := New(Config{Kind: "webdav", Endpoint: "https://dav.example/astr"}, nil, now)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	identity, err := first.Identity(context.Background())
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	again, _ := New(Config{Kind: "webdav", Endpoint: "https://dav.example/astr"}, nil, now)
	other, _ := New(Config{Kind: "webdav", Endpoint: "https://dav.example/other"}, nil, now)
	otherIdentity, _ := other.Identity(context.Background())
	if againIdentity, _ := again.Identity(context.Background()); againIdentity != identity {
		t.Fatalf("identity is not stable: %q vs %q", againIdentity, identity)
	}
	if otherIdentity == identity {
		t.Fatal("two locations share an identity")
	}
}

func TestNewRejectsUnknownKindAndBadEndpoint(t *testing.T) {
	if _, err := New(Config{Kind: "ftp", Endpoint: "ftp://x"}, nil, nil); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown kind = %v, want ErrUnsupported", err)
	}
	if _, err := New(Config{Kind: "webdav", Endpoint: "file:///tmp"}, nil, nil); err == nil {
		t.Fatal("a file:// endpoint was accepted")
	}
	if _, err := New(Config{Kind: "s3", Endpoint: "https://s3.example", Path: "bucket"}, nil, nil); err == nil {
		t.Fatal("an S3 remote without credentials was accepted")
	}
	if _, err := New(Config{
		Kind: "s3", Endpoint: "https://s3.example/dav", Path: "bucket", Addressing: "suffix",
		AccessKey: "key", SecretKey: "secret",
	}, nil, nil); err == nil {
		t.Fatal("a suffix endpoint with a path was accepted")
	}
}

// The path style keeps the bucket in the URL path, and keeps any path the
// endpoint carried, so a gateway reached under one is addressed as before.
func TestS3PathAddressingKeepsTheBucketInThePath(t *testing.T) {
	store, err := newS3(Config{
		Kind: "s3", Endpoint: "https://gateway.example/s3", Path: "bucket",
		AccessKey: "key", SecretKey: "secret",
	}, nil, nil)
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	if got := store.objectURL(packageName); got != "https://gateway.example/bucket/s3/"+packageName {
		t.Fatalf("object URL = %q", got)
	}
}

// The suffix style reads a bare domain as the base and builds the bucket into
// the host, which is what a store that only serves the virtual-host form
// expects.
func TestS3SuffixAddressingBuildsTheBucketIntoTheHost(t *testing.T) {
	store, err := newS3(Config{
		Kind: "s3", Endpoint: "s3.example.com", Path: "bucket", Region: "eu-west-1",
		AccessKey: "key", SecretKey: "secret", Addressing: "suffix",
	}, nil, nil)
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	if got := store.objectURL(packageName); got != "https://bucket.s3.s3.example.com/"+packageName {
		t.Fatalf("object URL = %q", got)
	}
	// The region still selects the signing scope, even though it is not in the
	// host.
	if store.region != "eu-west-1" {
		t.Fatalf("region = %q", store.region)
	}
}

// A bucket is a location, so switching how requests address it does not move
// the store: a package written under one style is still the same remote.
func TestS3IdentityDoesNotDependOnAddressing(t *testing.T) {
	config := Config{
		Kind: "s3", Endpoint: "https://s3.example", Path: "bucket",
		AccessKey: "key", SecretKey: "secret",
	}
	path, err := newS3(config, nil, nil)
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	config.Addressing = "suffix"
	suffix, err := newS3(config, nil, nil)
	if err != nil {
		t.Fatalf("new s3 suffix: %v", err)
	}
	if path.identity != suffix.identity {
		t.Fatalf("identity = %q vs %q", path.identity, suffix.identity)
	}
}

// decodeLock refuses an incomplete lock rather than treating it as absent, so
// a damaged lock object is visible instead of silently ignored.
func TestDecodeLockRejectsIncomplete(t *testing.T) {
	if _, err := decodeLock([]byte(`{"owner":""}`)); err == nil {
		t.Fatal("an ownerless lock was accepted")
	}
	if _, err := decodeLock([]byte(`not json`)); err == nil {
		t.Fatal("malformed lock was accepted")
	}
}

// The object path is absolute and encodes every segment the way Signature
// Version 4 does, so the path that is sent is the path that is signed.
func TestS3ObjectPathIsTheSignedPath(t *testing.T) {
	cases := []struct {
		endpoint   string
		addressing string
		want       string
	}{
		{"https://s3.example", "path", "/bucket/" + packageName},
		{"s3.example", "suffix", "/" + packageName},
		{"https://s3.example/team a/b+c//d$@", "path", "/bucket/team%20a/b%2Bc/d%24%40/" + packageName},
		{"https://s3.example/%E6%96%87", "path", "/bucket/%E6%96%87/" + packageName},
	}
	for _, testCase := range cases {
		store, err := newS3(Config{
			Kind: "s3", Endpoint: testCase.endpoint, Path: "bucket", Addressing: testCase.addressing,
			AccessKey: "key", SecretKey: "secret",
		}, nil, nil)
		if err != nil {
			t.Fatalf("new s3 %q: %v", testCase.endpoint, err)
		}
		if got := store.objectPath(packageName); got != testCase.want {
			t.Fatalf("object path for %q = %q, want %q", testCase.endpoint, got, testCase.want)
		}
	}
	// A folder typed after the bucket stays a folder.
	store, err := newS3(Config{
		Kind: "s3", Endpoint: "https://s3.example", Path: "bucket/team/",
		AccessKey: "key", SecretKey: "secret",
	}, nil, nil)
	if err != nil {
		t.Fatalf("new s3: %v", err)
	}
	if got := store.objectPath(packageName); got != "/bucket/team/"+packageName {
		t.Fatalf("object path = %q", got)
	}
}

// A dot segment would be resolved by the server or a proxy before it checks
// the signature, so the endpoint refuses one.
func TestS3RejectsDotSegmentsInTheEndpointPath(t *testing.T) {
	for _, location := range []struct{ endpoint, bucket string }{
		{"https://s3.example/a/../b", "bucket"},
		{"https://s3.example/./a", "bucket"},
		{"https://s3.example", "bucket/../other"},
	} {
		if _, err := newS3(Config{
			Kind: "s3", Endpoint: location.endpoint, Path: location.bucket,
			AccessKey: "key", SecretKey: "secret",
		}, nil, nil); err == nil {
			t.Fatalf("%q with bucket %q was accepted", location.endpoint, location.bucket)
		}
	}
}

// A server that verifies the signature the way AWS SigV4 defines it accepts
// every request, including one under a prefix with characters that need
// encoding, and receives If-Match as a quoted entity tag.
func TestS3SignatureIsAcceptedByASpecCompliantServer(t *testing.T) {
	const accessKey = "AKIDEXAMPLE"
	const secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	const region = "us-east-1"
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// The server decodes the path and encodes each segment again, as AWS
		// does, rather than trusting the client's encoding.
		segments := strings.Split(request.URL.Path, "/")
		for index, segment := range segments {
			segments[index] = strings.ReplaceAll(url.QueryEscape(segment), "+", "%20")
		}
		canonicalPath := strings.Join(segments, "/")
		paths = append(paths, canonicalPath)
		authorization := request.Header.Get("Authorization")
		signedHeaders := authorization[strings.Index(authorization, "SignedHeaders=")+len("SignedHeaders="):]
		signedHeaders = signedHeaders[:strings.Index(signedHeaders, ",")]
		var canonicalHeaders strings.Builder
		for _, name := range strings.Split(signedHeaders, ";") {
			value := request.Header.Get(name)
			if name == "host" {
				value = request.Host
			}
			canonicalHeaders.WriteString(name + ":" + value + "\n")
		}
		amzDate := request.Header.Get("X-Amz-Date")
		payloadHash := request.Header.Get("X-Amz-Content-Sha256")
		canonicalRequest := strings.Join([]string{
			request.Method,
			canonicalPath,
			request.URL.RawQuery,
			canonicalHeaders.String(),
			signedHeaders,
			payloadHash,
		}, "\n")
		scope := amzDate[:8] + "/" + region + "/s3/aws4_request"
		requestHash := sha256.Sum256([]byte(canonicalRequest))
		stringToSign := strings.Join([]string{
			"AWS4-HMAC-SHA256",
			amzDate,
			scope,
			hex.EncodeToString(requestHash[:]),
		}, "\n")
		key := hmacSHA256([]byte("AWS4"+secretKey), amzDate[:8])
		key = hmacSHA256(key, region)
		key = hmacSHA256(key, "s3")
		key = hmacSHA256(key, "aws4_request")
		expected := hex.EncodeToString(hmacSHA256(key, stringToSign))
		if !strings.HasSuffix(authorization, "Signature="+expected) {
			writer.WriteHeader(http.StatusForbidden)
			return
		}
		if request.Method == http.MethodPut {
			if ifMatch := request.Header.Get("If-Match"); ifMatch != `"v1"` {
				writer.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			writer.Header().Set("ETag", `"v2"`)
			writer.WriteHeader(http.StatusOK)
			return
		}
		writer.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	for _, endpoint := range []string{server.URL, server.URL + "/team a/b+c$@"} {
		store, err := newS3(Config{
			Kind: "s3", Endpoint: endpoint, Path: "bucket", Region: region,
			AccessKey: accessKey, SecretKey: secretKey,
		}, server.Client(), time.Now)
		if err != nil {
			t.Fatalf("new s3: %v", err)
		}
		if _, _, exists, err := store.Read(context.Background()); err != nil || exists {
			t.Fatalf("read under %q = %v, %v; a server that signs the way AWS SigV4 defines it refused the request", endpoint, exists, err)
		}
		// An ETag an earlier version stored from a weak response still matches.
		if etag, err := store.Write(context.Background(), []byte("package"), `W/"v1`); err != nil || etag != "v2" {
			t.Fatalf("write under %q = %q, %v", endpoint, etag, err)
		}
	}
	if want := "/bucket/team%20a/b%2Bc%24%40/" + packageName; paths[len(paths)-1] != want {
		t.Fatalf("server saw %q, want %q", paths[len(paths)-1], want)
	}
}
