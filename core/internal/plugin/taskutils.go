package plugin

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MoeForge/moejs"
	"github.com/MoeForge/moejs/engine"
)

// TaskFetch lets a driver hook of a task plugin read upstream data with
// utils.fetch: GET or HEAD to the hosts its request descriptors may
// address, through the gateway's proxy.
type TaskFetch struct {
	// Allowed reports whether a request may go to target.
	Allowed func(target *url.URL) bool
	// Client sends the requests.
	Client *http.Client
	// Post lets the hook POST a body, as the request processing hooks
	// may; Refs turns a file reference of the call into its data URL, for
	// the JSON string values of a body that name one.
	Post bool
	Refs func(ref string) (string, bool)
}

// Bounds of utils.fetch within one hook call, and of utils.json.clone.
const (
	maxTaskFetches      = 4
	taskFetchTimeout    = 15 * time.Second
	taskFetchBudget     = 30 * time.Second
	maxTaskFetchBody    = 1 << 20
	maxTaskFetchURL     = 64 << 10
	maxTaskFetchHeaders = 64
	maxTaskHeaderValue  = 8 << 10
	maxCloneBytes       = 1 << 20
	maxCloneNodes       = 32768
	maxCloneDepth       = 32
)

// taskFetchForbidden are the headers utils.fetch leaves to the transport.
var taskFetchForbidden = []string{"host", "content-length", "accept-encoding", "connection", "proxy-connection", "keep-alive", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade"}

// installTask adds new-api's utils and a console that writes the plugin's
// journal to a task plugin's runtime.
func (bound *instance) installTask() error {
	runtime := bound.runtime
	fail := func(realm *moejs.Realm, format string, args ...any) error {
		return realm.Throw(engine.ObjectValue(realm.NewError(engine.KindError, format, args...)))
	}
	text := func(realm *moejs.Realm, args []moejs.Value, index int, name string) (string, error) {
		value := moejs.Arg(args, index)
		if !value.IsString() {
			return "", realm.TypeError("utils: the %s must be a string", name)
		}
		converted, err := realm.ToString(value)
		if err != nil {
			return "", err
		}
		return converted.GoString(), nil
	}
	console := map[string]any{}
	for name, level := range map[string]string{"log": "info", "info": "info", "debug": "debug", "warn": "warn", "error": "warn"} {
		console[name] = runtime.Function(name, 0, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil {
				return moejs.Undefined(), nil
			}
			parts := make([]string, 0, len(args))
			for _, arg := range args {
				if arg.IsString() {
					parts = append(parts, arg.String())
					continue
				}
				var raw json.RawMessage
				if runtime.UnmarshalWith(arg, &raw, moejs.DecodeOptions{MaxBytes: 4 << 10, MaxNodes: 512}) == nil && len(raw) > 0 {
					parts = append(parts, string(raw))
				} else {
					parts = append(parts, realm.DisplayString(arg))
				}
			}
			return hostLog(c, realm, []moejs.Value{moejs.String(level), moejs.String(strings.Join(parts, " "))})
		})
	}
	if err := runtime.SetGlobal("console", console); err != nil {
		return err
	}
	utils := map[string]any{
		"hasCapability": runtime.Function("hasCapability", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			name, err := text(realm, args, 0, "capability")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.Bool(slices.Contains(taskCapabilities, name)), nil
		}),
		"json": map[string]any{
			"clone": runtime.Function("clone", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
				value := moejs.Arg(args, 0)
				if err := plainJSONValue(realm, value); err != nil {
					return moejs.Undefined(), fail(realm, "utils.json.clone: %v", err)
				}
				var raw json.RawMessage
				if err := runtime.UnmarshalWith(value, &raw, moejs.DecodeOptions{MaxBytes: maxCloneBytes, MaxNodes: maxCloneNodes}); err != nil || len(raw) == 0 {
					return moejs.Undefined(), fail(realm, "utils.json.clone: the value is larger than 1 MiB")
				}
				return realm.JSONParseGoString(string(raw))
			}),
		},
		"unixNow": runtime.Function("unixNow", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			now := time.Now()
			if c := bound.current; c != nil {
				now = c.now()
			}
			return moejs.Int(now.Unix()), nil
		}),
		"uuid": runtime.Function("uuid", 0, func(*moejs.Realm, moejs.Value, []moejs.Value) (moejs.Value, error) {
			return moejs.String(randomUUID()), nil
		}),
		"hmacSHA256": runtime.Function("hmacSHA256", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			message, err := text(realm, args, 0, "message")
			if err != nil {
				return moejs.Undefined(), err
			}
			secret, err := text(realm, args, 1, "secret")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.String(hex.EncodeToString(hmacSum([]byte(secret), []byte(message)))), nil
		}),
		"jwtSignHS256": runtime.Function("jwtSignHS256", 2, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			var raw json.RawMessage
			if err := runtime.UnmarshalWith(moejs.Arg(args, 0), &raw, moejs.DecodeOptions{MaxBytes: 64 << 10, MaxNodes: 4096}); err != nil || len(raw) == 0 || raw[0] != '{' {
				return moejs.Undefined(), fail(realm, "utils.jwtSignHS256: the claims must be a plain object")
			}
			// The claims are encoded as Go encodes a map, by sorted key, with
			// each number as written.
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			var members map[string]any
			if err := decoder.Decode(&members); err != nil {
				return moejs.Undefined(), fail(realm, "utils.jwtSignHS256: the claims must be a plain object")
			}
			claims, _ := json.Marshal(members)
			secret, err := text(realm, args, 1, "secret")
			if err != nil {
				return moejs.Undefined(), err
			}
			encode := base64.RawURLEncoding.EncodeToString
			unsigned := encode([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + encode(claims)
			return moejs.String(unsigned + "." + encode(hmacSum([]byte(secret), []byte(unsigned)))), nil
		}),
		"base64": runtime.Function("base64", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			value, err := text(realm, args, 0, "value")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.String(base64.StdEncoding.EncodeToString([]byte(value))), nil
		}),
		"base64URL": runtime.Function("base64URL", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			value, err := text(realm, args, 0, "value")
			if err != nil {
				return moejs.Undefined(), err
			}
			return moejs.String(base64.RawURLEncoding.EncodeToString([]byte(value))), nil
		}),
		"base64URLDecode": runtime.Function("base64URLDecode", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			value, err := text(realm, args, 0, "value")
			if err != nil {
				return moejs.Undefined(), err
			}
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
			if err != nil || !utf8.Valid(decoded) {
				return moejs.Undefined(), fail(realm, "utils.base64URLDecode: the value is not base64url text")
			}
			return moejs.String(string(decoded)), nil
		}),
		"volcSignV4": runtime.Function("volcSignV4", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			var raw map[string]any
			if err := runtime.UnmarshalWith(moejs.Arg(args, 0), &raw, moejs.DecodeOptions{MaxBytes: 4 << 20, MaxNodes: 4096}); err != nil {
				return moejs.Undefined(), fail(realm, "utils.volcSignV4: the request must be a plain object")
			}
			signed, err := volcSignV4(raw, time.Now())
			if err != nil {
				return moejs.Undefined(), fail(realm, "utils.volcSignV4: %v", err)
			}
			return realm.FromGo(signed)
		}),
		"fetch": runtime.Function("fetch", 1, func(realm *moejs.Realm, _ moejs.Value, args []moejs.Value) (moejs.Value, error) {
			c := bound.current
			if c == nil || c.options.TaskFetch == nil {
				name := "the top level"
				if c != nil {
					name = c.hook
				}
				return moejs.Undefined(), fail(realm, "utils.fetch is not available in %s", name)
			}
			return taskFetch(c, realm, moejs.Arg(args, 0))
		}),
	}
	for name, function := range bound.requestUtils() {
		utils[name] = function
	}
	return runtime.SetGlobal("utils", utils)
}

func hmacSum(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// plainJSONValue checks that value is what utils.json.clone takes: null,
// booleans, finite numbers and strings in plain objects and dense arrays,
// at most maxCloneDepth levels below the root, maxCloneNodes values in all,
// and no cycles.
func plainJSONValue(realm *moejs.Realm, value moejs.Value) error {
	nodes := 0
	path := map[*engine.Object]bool{}
	var walk func(value moejs.Value, depth int) error
	walk = func(value moejs.Value, depth int) error {
		if nodes++; nodes > maxCloneNodes {
			return errors.New("the value has more than 32,768 values")
		}
		if depth > maxCloneDepth {
			return errors.New("the value is nested deeper than 32 levels")
		}
		switch {
		case value.IsNull(), value.IsBool(), value.IsString():
			return nil
		case value.IsNumber():
			if number := value.AsNumber(); math.IsNaN(number) || math.IsInf(number, 0) {
				return errors.New("a number is not finite")
			}
			return nil
		case value.IsUndefined():
			return errors.New("undefined is not JSON")
		case value.IsBigInt():
			return errors.New("a BigInt is not JSON")
		case !value.IsObject():
			return errors.New("a symbol is not JSON")
		}
		object := value.AsObject()
		if path[object] {
			return errors.New("the value is cyclic")
		}
		path[object] = true
		defer delete(path, object)
		switch {
		case object.IsCallable():
			return errors.New("a function is not JSON")
		case object.Class() == engine.ClassArray:
			if !object.IsDenseArray() {
				return errors.New("a sparse array is not JSON")
			}
			for index := range object.ArrayLength() {
				element, err := object.GetIndex(realm, index)
				if err != nil {
					return err
				}
				if err := walk(element, depth+1); err != nil {
					return err
				}
			}
			return nil
		case object.Class() != engine.ClassObject || object.Proto() != nil && object.Proto() != realm.ObjectPrototype:
			return errors.New("a " + object.ClassName() + " is not JSON")
		}
		keys, err := realm.EnumerableOwnKeys(object)
		if err != nil {
			return err
		}
		for _, key := range keys {
			member, err := object.Get(realm, key, value)
			if err != nil {
				return err
			}
			if err := walk(member, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(value, 0)
}

// taskFetch serves utils.fetch: one GET or HEAD to an allowed host, waited
// on outside the call's script time and execution slot.
func taskFetch(c *call, realm *moejs.Realm, argument moejs.Value) (moejs.Value, error) {
	fail := func(format string, args ...any) (moejs.Value, error) {
		return moejs.Undefined(), realm.Throw(engine.ObjectValue(realm.NewError(engine.KindError, "utils.fetch: "+format, args...)))
	}
	var raw map[string]json.RawMessage
	if err := c.runtime.UnmarshalWith(argument, &raw, moejs.DecodeOptions{MaxBytes: 1 << 20, MaxNodes: 1024}); err != nil || raw == nil {
		return fail("the request must be a plain object")
	}
	var request struct {
		URL          string            `json:"url"`
		Method       string            `json:"method"`
		Headers      map[string]string `json:"headers"`
		ResponseType string            `json:"responseType"`
		Body         *string           `json:"body"`
	}
	post := c.options.TaskFetch.Post
	for name := range raw {
		if !slices.Contains([]string{"url", "method", "headers", "responseType"}, name) && !(post && name == "body") {
			if post {
				return fail("the request has the member %q; it takes url, method, headers, responseType and body", name)
			}
			return fail("the request has the member %q; it takes url, method, headers and responseType", name)
		}
	}
	text, _ := json.Marshal(raw)
	if err := json.Unmarshal(text, &request); err != nil {
		return fail("the request is malformed")
	}
	method := strings.ToUpper(request.Method)
	if method == "" {
		method = http.MethodGet
	}
	switch {
	case post && method != http.MethodGet && method != http.MethodHead && method != http.MethodPost:
		return fail("the method must be GET, HEAD or POST")
	case !post && method != http.MethodGet && method != http.MethodHead:
		return fail("the method must be GET or HEAD")
	case request.Body != nil && method != http.MethodPost:
		return fail("only a POST has a body")
	case request.Body != nil && len(*request.Body) > maxTaskFetchBody:
		return fail("the body is larger than 1 MiB")
	}
	var payload []byte
	if request.Body != nil {
		payload = []byte(*request.Body)
		if refs := c.options.TaskFetch.Refs; refs != nil {
			replaced, err := withFileRefs(payload, refs)
			if err != nil {
				return fail("%v", err)
			}
			payload = replaced
		}
	}
	if request.ResponseType != "" && request.ResponseType != "json" && request.ResponseType != "bytes" {
		return fail("the responseType must be json or bytes")
	}
	target, err := url.Parse(request.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || len(request.URL) > maxTaskFetchURL {
		return fail("the url must be an absolute http or https URL of at most 64 KiB")
	}
	if len(request.Headers) > maxTaskFetchHeaders {
		return fail("a request has at most 64 headers")
	}
	for name, value := range request.Headers {
		if slices.Contains(taskFetchForbidden, strings.ToLower(name)) || len(value) > maxTaskHeaderValue || strings.ContainsAny(value, "\r\n") || !headerNamePattern.MatchString(name) {
			return fail("the header %q is not one a plugin sets", name)
		}
	}
	if !c.options.TaskFetch.Allowed(target) {
		return fail("the host %s is not allowed", target.Hostname())
	}
	if c.taskFetches >= maxTaskFetches {
		return fail("a hook call fetches at most %d times", maxTaskFetches)
	}
	left := taskFetchBudget - c.taskFetchTime
	if left <= 0 {
		return fail("the hook call has no fetch time left")
	}
	c.taskFetches++
	var status int
	var header http.Header
	var body []byte
	var fetchErr error
	started := time.Now()
	if err := c.wait(func() {
		ctx, cancel := context.WithTimeout(c.ctx, min(taskFetchTimeout, left))
		defer cancel()
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		outgoing, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
		if err != nil {
			fetchErr = err
			return
		}
		for name, value := range request.Headers {
			outgoing.Header.Set(name, value)
		}
		if outgoing.Header.Get("User-Agent") == "" {
			outgoing.Header.Set("User-Agent", "")
		}
		client := *c.options.TaskFetch.Client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(outgoing)
		if err != nil {
			fetchErr = err
			return
		}
		defer response.Body.Close()
		status, header = response.StatusCode, response.Header
		body, err = io.ReadAll(io.LimitReader(response.Body, maxTaskFetchBody+1))
		if err != nil {
			fetchErr = err
		}
	}); err != nil {
		return moejs.Undefined(), err
	}
	c.taskFetchTime += time.Since(started)
	switch {
	case fetchErr != nil && errors.Is(fetchErr, context.DeadlineExceeded):
		return fail("the request to %s timed out", target.Hostname())
	case fetchErr != nil:
		return fail("the request to %s failed", target.Hostname())
	case len(body) > maxTaskFetchBody:
		return fail("the response of %s is larger than 1 MiB", target.Hostname())
	}
	headers := map[string]any{}
	for name, values := range header {
		if len(values) > 0 {
			headers[http.CanonicalHeaderKey(name)] = values[0]
		}
	}
	result := realm.NewObject()
	set := func(key string, value moejs.Value) {
		_, _ = result.CreateDataProperty(realm, realm.KeyFromGoString(key), value)
	}
	set("status", moejs.Int(int64(status)))
	headerValue, err := realm.FromGo(headers)
	if err != nil {
		return moejs.Undefined(), err
	}
	set("headers", headerValue)
	switch {
	case request.ResponseType == "bytes":
		buffer, err := bytesValue(realm, body)
		if err != nil {
			return moejs.Undefined(), err
		}
		set("body", buffer)
	case method == http.MethodHead:
		set("body", moejs.String(""))
	default:
		if json.Valid(body) {
			parsed, err := realm.JSONParseGoString(string(body))
			if err != nil {
				return moejs.Undefined(), err
			}
			set("body", parsed)
		} else {
			set("body", moejs.String(strings.ToValidUTF8(string(body), "�")))
		}
	}
	return engine.ObjectValue(result), nil
}

// Bounds of the files a request processing hook sends with utils.fetch.
const (
	maxFetchRefs     = 8
	maxFetchRefBytes = 32 << 20
)

// withFileRefs replaces each JSON string value of body that is a file
// reference of the call with the file's data URL. A body that is not JSON
// goes as it is.
func withFileRefs(body []byte, refs func(string) (string, bool)) ([]byte, error) {
	if !bytes.Contains(body, []byte("[media:")) || !json.Valid(body) {
		return body, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return body, nil
	}
	count, size := 0, 0
	var walk func(any) (any, error)
	walk = func(node any) (any, error) {
		switch typed := node.(type) {
		case string:
			if !strings.HasPrefix(typed, "[media:") {
				return typed, nil
			}
			data, ok := refs(typed)
			if !ok {
				return typed, nil
			}
			count++
			size += len(data)
			if count > maxFetchRefs || size > maxFetchRefBytes {
				return nil, fmt.Errorf("a body sends at most %d files and 32 MiB of them", maxFetchRefs)
			}
			return data, nil
		case map[string]any:
			for key, member := range typed {
				replaced, err := walk(member)
				if err != nil {
					return nil, err
				}
				typed[key] = replaced
			}
		case []any:
			for index, item := range typed {
				replaced, err := walk(item)
				if err != nil {
					return nil, err
				}
				typed[index] = replaced
			}
		}
		return node, nil
	}
	replaced, err := walk(value)
	if err != nil || count == 0 {
		return body, err
	}
	return json.Marshal(replaced)
}

// volcSignV4 signs a request for Volcengine's OpenAPI with its HMAC-SHA256
// scheme and returns the headers to add: Authorization, X-Date and
// X-Content-Sha256. The members may be named as in new-api's types or with
// capitals.
func volcSignV4(raw map[string]any, now time.Time) (map[string]string, error) {
	value := func(names ...string) any {
		for _, name := range names {
			if found, ok := raw[name]; ok {
				return found
			}
		}
		return nil
	}
	str := func(names ...string) string {
		text, _ := value(names...).(string)
		return text
	}
	method := strings.ToUpper(str("method", "Method"))
	target, err := url.Parse(str("url", "URL", "Url"))
	if err != nil || target.Host == "" {
		return nil, errors.New("the url must be absolute")
	}
	accessKey, secretKey := str("accessKey", "AccessKey"), str("secretKey", "SecretKey")
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("accessKey and secretKey are required")
	}
	region := str("region", "Region")
	if region == "" {
		region = "cn-north-1"
	}
	service := str("service", "Service")
	if service == "" {
		service = "cv"
	}
	if stamp, ok := value("timestamp", "Timestamp").(float64); ok && stamp > 0 {
		now = time.Unix(int64(stamp), 0)
	}
	if method == "" {
		method = http.MethodPost
	}
	body := str("body", "Body")
	headers := map[string]string{}
	if given, ok := value("headers", "Headers").(map[string]any); ok {
		for name, header := range given {
			if text, ok := header.(string); ok {
				headers[strings.ToLower(name)] = strings.TrimSpace(text)
			}
		}
	}
	xDate := now.UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	payloadHash := hex.EncodeToString(sha256Sum([]byte(body)))
	headers["host"] = target.Host
	headers["x-date"] = xDate
	headers["x-content-sha256"] = payloadHash
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	query := target.Query()
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var pairs []string
	for _, key := range keys {
		values := slices.Clone(query[key])
		sort.Strings(values)
		for _, item := range values {
			pairs = append(pairs, volcEscape(key)+"="+volcEscape(item))
		}
	}
	canonical := strings.Join([]string{method, path, strings.Join(pairs, "&"), canonicalHeaders.String(), signedHeaders, payloadHash}, "\n")
	scope := shortDate + "/" + region + "/" + service + "/request"
	toSign := "HMAC-SHA256\n" + xDate + "\n" + scope + "\n" + hex.EncodeToString(sha256Sum([]byte(canonical)))
	key := hmacSum([]byte(secretKey), []byte(shortDate))
	key = hmacSum(key, []byte(region))
	key = hmacSum(key, []byte(service))
	key = hmacSum(key, []byte("request"))
	signature := hex.EncodeToString(hmacSum(key, []byte(toSign)))
	return map[string]string{
		"Authorization":    fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", accessKey, scope, signedHeaders, signature),
		"X-Date":           xDate,
		"X-Content-Sha256": payloadHash,
	}, nil
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

// volcEscape percent-encodes as Volcengine's canonical query does: every
// byte but the unreserved characters.
func volcEscape(text string) string {
	var builder strings.Builder
	for _, char := range []byte(text) {
		if 'A' <= char && char <= 'Z' || 'a' <= char && char <= 'z' || '0' <= char && char <= '9' || char == '-' || char == '_' || char == '.' || char == '~' {
			builder.WriteByte(char)
			continue
		}
		builder.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(char)|0x100, 16)[1:]))
	}
	return builder.String()
}
