package plugincli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	// A case's time zone loads on computers without a zone database too.
	_ "time/tzdata"

	"github.com/QuantumNous/astrlink/core/internal/plugin"
)

// fixture is one case of a project's tests directory: a hook to call with
// arguments, what it runs with, and what it must return.
type fixture struct {
	Name string            `json:"name"`
	Hook string            `json:"hook"`
	Args []json.RawMessage `json:"args"`
	setup
	Expect expectation `json:"expect"`
	// Member, Expected and ExpectedError are new-api's fixture format, so a
	// task plugin's author tests with the same cases on both: the member of
	// the hook to call (a member of native, or of a protocol of protocols,
	// such as openai_video.render), the result and a text of the error.
	Member        string          `json:"member"`
	Expected      json.RawMessage `json:"expected"`
	ExpectedError string          `json:"expectedError"`
	file          string
}

// normalize reads a case in new-api's format as one of ours: the hook at
// the member's path, and what it expects where it says nothing itself.
func (each *fixture) normalize() {
	if each.Member != "" {
		each.Hook += "." + each.Member
		each.Member = ""
	}
	if len(each.Expected) > 0 && len(each.Expect.Result) == 0 && len(each.Expect.Match) == 0 {
		each.Expect.Result = each.Expected
	}
	if each.ExpectedError != "" && each.Expect.Error == "" {
		each.Expect.Error = each.ExpectedError
	}
}

// setup is what a case runs with: the settings and stored values, the
// responses its requests get, the clock, and the sessions' use of the
// plugin's attributes. A file of cases may give it to all of them.
type setup struct {
	// Config is the plugin's configuration. Capabilities are what earlier
	// versions granted one by one; a plugin now has every capability it
	// declares, and the field is read and left alone.
	Config       json.RawMessage `json:"config"`
	Capabilities []string        `json:"capabilities"`
	// Files are the files of the plugin's data directory, its working
	// directory, when the case starts: text by path.
	Files map[string]string `json:"files"`
	// Request is what astrlink.request.info returns.
	Request *plugin.RequestInfo        `json:"request"`
	Store   map[string]json.RawMessage `json:"store"`
	Fetch   []fakeResponse             `json:"fetch"`
	// Now is the time the clock says, in RFC 3339; the real time when
	// absent. TimeZone names the zone astrlink.clock.offset follows, such as
	// America/New_York; the computer's when absent.
	Now      string `json:"now"`
	TimeZone string `json:"timeZone"`
	// MetaValues are what astrlink.meta.values returns, by attribute key.
	MetaValues map[string][]plugin.ValueStat `json:"metaValues"`
}

// fixtureFile is a file of cases that share a setup: a case takes the
// file's value of what it does not set itself, and its requests get its own
// responses first, then the file's.
type fixtureFile struct {
	setup
	Cases []fixture `json:"cases"`
}

// within returns the case's setup completed from the file's.
func (own setup) within(file setup) setup {
	if own.Config == nil {
		own.Config = file.Config
	}
	if own.Capabilities == nil {
		own.Capabilities = file.Capabilities
	}
	if own.Request == nil {
		own.Request = file.Request
	}
	if own.Store == nil {
		own.Store = file.Store
	}
	if own.Now == "" {
		own.Now = file.Now
	}
	if own.TimeZone == "" {
		own.TimeZone = file.TimeZone
	}
	if own.MetaValues == nil {
		own.MetaValues = file.MetaValues
	}
	if own.Files == nil {
		own.Files = file.Files
	}
	own.Fetch = append(slices.Clip(own.Fetch), file.Fetch...)
	return own
}

// fakeResponse answers the requests whose method and address match: the
// address exactly, or as a prefix when it ends with *.
type fakeResponse struct {
	Method string `json:"method"`
	URL    string `json:"url"`
	// Body, when set, answers only a request whose JSON body holds these
	// members with these values.
	Body json.RawMessage `json:"body"`
	// Times, when set, is how many requests the response answers: later ones
	// go to the next response that matches, and a case that makes fewer
	// fails.
	Times   int               `json:"times"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	JSON    json.RawMessage   `json:"json"`
	Text    *string           `json:"text"`
	// Error fails the request as the host would: denied, timeout, network.
	Error string `json:"error"`
	// DelayMs holds the response back, and is the time the request took.
	DelayMs int `json:"delayMs"`
}

// describe names a response in a failure.
func (response fakeResponse) describe() string {
	method := response.Method
	if method == "" {
		method = "any"
	}
	return method + " " + response.URL
}

// expectation is what a case checks: the result in full or in part, or the
// failure, and the stored values after the call, null for a key that holds
// none.
type expectation struct {
	Result json.RawMessage            `json:"result"`
	Match  json.RawMessage            `json:"match"`
	Error  string                     `json:"error"`
	Store  map[string]json.RawMessage `json:"store"`
	// Files are the files of the plugin's data directory after the call:
	// text by path, null for one that must not be there.
	Files map[string]*string `json:"files"`
}

type caseOutcome struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func runTest(ctx context.Context, env Environment, args []string) int {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	only := flags.String("run", "", "run only the cases whose name contains this text")
	file := flags.String("fixture", "", "run the cases of this file instead of tests/*.json")
	positional, ok := parse(env, "test", flags, args, 1)
	if !ok {
		return 2
	}
	dir := projectDir(positional)
	var fixtures []fixture
	var err error
	if *file != "" {
		fixtures, err = readFixtureFile(*file)
	} else {
		fixtures, err = readFixtures(filepath.Join(dir, "tests"))
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	if *only != "" {
		kept := fixtures[:0]
		for _, each := range fixtures {
			if strings.Contains(each.Name, *only) {
				kept = append(kept, each)
			}
		}
		fixtures = kept
	}
	if len(fixtures) == 0 {
		where := "tests/*.json"
		if *file != "" {
			where = *file
		}
		fmt.Fprintf(env.Stderr, "error: no test cases in %s\n", where)
		return 1
	}
	project, ok := buildProject(env, dir)
	if !ok {
		return 1
	}
	runner, err := newRunner(project)
	if err != nil {
		fmt.Fprintf(env.Stderr, "error: %v\n", err)
		return 1
	}
	defer runner.close()
	outcomes := make([]caseOutcome, 0, len(fixtures))
	failed := 0
	for _, each := range fixtures {
		outcome := runner.run(ctx, each)
		outcomes = append(outcomes, outcome)
		status := "PASS"
		if !outcome.OK {
			status = "FAIL"
			failed++
		}
		fmt.Fprintf(env.Stderr, "%s  %s", status, outcome.Name)
		if outcome.Message != "" {
			fmt.Fprintf(env.Stderr, "\n      %s", strings.ReplaceAll(outcome.Message, "\n", "\n      "))
		}
		fmt.Fprintln(env.Stderr)
	}
	printJSON(env.Stdout, map[string]any{"passed": len(outcomes) - failed, "failed": failed, "cases": outcomes})
	if failed > 0 {
		return 1
	}
	return 0
}

// readFixtures reads every case of the *.json files in dir: a file holds
// one case, an array of them, or an object whose cases share its setup.
func readFixtures(dir string) ([]fixture, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var fixtures []fixture
	for _, name := range names {
		cases, err := readFixtureFile(name)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, cases...)
	}
	return fixtures, nil
}

// readFixtureFile reads the cases of one file.
func readFixtureFile(name string) ([]fixture, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	cases, err := parseFixtures(bytes.TrimSpace(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Base(name), err)
	}
	for index := range cases {
		cases[index].file = filepath.Base(name)
		if cases[index].Name == "" {
			cases[index].Name = fmt.Sprintf("%s #%d", filepath.Base(name), index+1)
		}
		if cases[index].Hook == "" {
			return nil, fmt.Errorf("%s: case %q names no hook", filepath.Base(name), cases[index].Name)
		}
		cases[index].normalize()
	}
	return cases, nil
}

func parseFixtures(data []byte) ([]fixture, error) {
	if bytes.HasPrefix(data, []byte("[")) {
		var cases []fixture
		err := json.Unmarshal(data, &cases)
		return cases, err
	}
	var shape struct {
		Cases json.RawMessage `json:"cases"`
	}
	if err := json.Unmarshal(data, &shape); err != nil {
		return nil, err
	}
	if shape.Cases == nil {
		var single fixture
		err := json.Unmarshal(data, &single)
		return []fixture{single}, err
	}
	var file fixtureFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	for index := range file.Cases {
		file.Cases[index].setup = file.Cases[index].setup.within(file.setup)
	}
	return file.Cases, nil
}

// runner runs cases against one build of the plugin, each in a registry of
// its own. The build sits in an install directory under root, and each case
// gets a data directory of its own there.
type runner struct {
	key  string
	root string
	// settings, the responses and the store are the current case's;
	// answered counts the requests each response answered.
	mutex      sync.Mutex
	settings   plugin.Settings
	responses  []fakeResponse
	answered   []int
	unexpected []string
	store      *memoryStore
}

func newRunner(project *built) (*runner, error) {
	root, err := os.MkdirTemp("", "astrlink-plugin-test-")
	if err != nil {
		return nil, err
	}
	current := &runner{key: project.checked.Meta.Key, root: root}
	if _, err := project.result.Write(filepath.Join(root, "plugins", current.key)); err != nil {
		current.close()
		return nil, err
	}
	return current, nil
}

// close removes the build and the data directories.
func (current *runner) close() { _ = os.RemoveAll(current.root) }

// services are what the host serves the plugin in a case.
func (current *runner) services(each fixture) (plugin.Services, error) {
	services := plugin.Services{
		Fetcher:  current,
		Store:    storeFunc(func() *memoryStore { return current.store }),
		Metadata: metaValues(each.MetaValues),
	}
	if each.Now != "" {
		now, err := time.Parse(time.RFC3339Nano, each.Now)
		if err != nil {
			return services, fmt.Errorf("now %q is not an RFC 3339 time", each.Now)
		}
		services.Now = func() time.Time { return now }
	}
	if each.TimeZone != "" {
		zone, err := time.LoadLocation(each.TimeZone)
		if err != nil {
			return services, fmt.Errorf("timeZone %q: %v", each.TimeZone, err)
		}
		services.Zone = zone
	}
	return services, nil
}

func (current *runner) run(ctx context.Context, each fixture) caseOutcome {
	outcome := caseOutcome{Name: each.Name, File: each.file}
	services, err := current.services(each)
	if err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	current.mutex.Lock()
	current.settings = plugin.Settings{}
	current.responses = each.Fetch
	current.answered = make([]int, len(each.Fetch))
	current.unexpected = nil
	current.store = &memoryStore{values: map[string]json.RawMessage{}}
	for key, value := range each.Store {
		current.store.values[key] = value
	}
	current.mutex.Unlock()
	data, err := os.MkdirTemp(current.root, "data-")
	if err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	defer os.RemoveAll(data)
	for name, text := range each.Files {
		path := filepath.Join(data, current.key, filepath.FromSlash(name))
		if rel, err := filepath.Rel(filepath.Join(data, current.key), path); err != nil || strings.HasPrefix(rel, "..") {
			outcome.Message = fmt.Sprintf("files: %q is outside the data directory", name)
			return outcome
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			outcome.Message = err.Error()
			return outcome
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			outcome.Message = err.Error()
			return outcome
		}
	}
	manager := plugin.NewManager(plugin.ManagerOptions{
		Dir:     filepath.Join(current.root, "plugins"),
		DataDir: data,
		Settings: settingsFunc(func() map[string]plugin.Settings {
			current.mutex.Lock()
			defer current.mutex.Unlock()
			return map[string]plugin.Settings{current.key: current.settings}
		}),
		Services: services,
	})
	if err := manager.Reload(ctx); err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	loaded := manager.Current().Plugin(current.key)
	if loaded == nil || !loaded.Loaded() {
		outcome.Message = "the plugin did not load"
		return outcome
	}
	capabilities := each.Capabilities
	if capabilities == nil {
		capabilities = loaded.Meta.Capabilities
	}
	current.mutex.Lock()
	current.settings = plugin.Settings{Enabled: true, Capabilities: capabilities, Config: each.Config}
	current.mutex.Unlock()
	if err := manager.Refresh(ctx); err != nil {
		outcome.Message = err.Error()
		return outcome
	}
	loaded = manager.Current().Plugin(current.key)
	args := make([]any, 0, len(each.Args))
	for _, arg := range each.Args {
		args = append(args, arg)
	}
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, info, err := loaded.CallArgs(callCtx, each.Hook, args, plugin.CallOptions{Request: each.Request, NetworkBudget: 20 * time.Second})
	var problems []string
	current.mutex.Lock()
	problems = append(problems, current.unexpected...)
	for index, response := range current.responses {
		if response.Times > 0 && current.answered[index] < response.Times {
			problems = append(problems, fmt.Sprintf("%s answered %d of the %d requests it expects", response.describe(), current.answered[index], response.Times))
		}
	}
	current.mutex.Unlock()
	var failure *plugin.CallError
	switch {
	case each.Expect.Error != "":
		if !errors.As(err, &failure) {
			problems = append(problems, fmt.Sprintf("returned %s, want a failure mentioning %q", orUndefined(result), each.Expect.Error))
		} else if !strings.Contains(failure.Message, each.Expect.Error) && failure.Reason != each.Expect.Error {
			problems = append(problems, fmt.Sprintf("failed with %s: %s, want a failure mentioning %q", failure.Reason, failure.Message, each.Expect.Error))
		}
	case err != nil:
		message := err.Error()
		if errors.As(err, &failure) {
			message = failure.Reason + ": " + failure.Message
			if stack := loaded.Stack(failure); stack != "" {
				message += "\n" + stack
			}
		}
		problems = append(problems, "failed: "+message)
	case len(each.Expect.Result) > 0:
		// A hook that returns nothing returns null, as JSON has it.
		if len(result) == 0 {
			result = json.RawMessage("null")
		}
		if !jsonEqual(result, each.Expect.Result) {
			problems = append(problems, fmt.Sprintf("returned %s\nwant     %s", orUndefined(result), compact(each.Expect.Result)))
		}
	case len(each.Expect.Match) > 0:
		if !jsonMatches(result, each.Expect.Match) {
			problems = append(problems, fmt.Sprintf("returned %s\nwant it to contain %s", orUndefined(result), compact(each.Expect.Match)))
		}
	}
	for key, want := range each.Expect.Store {
		got, found := current.store.values[key]
		if !found && string(want) == "null" {
			continue
		}
		if !found || !jsonEqual(got, want) {
			problems = append(problems, fmt.Sprintf("stored %s = %s, want %s", key, orUndefined(got), compact(want)))
		}
	}
	for name, want := range each.Expect.Files {
		data, err := os.ReadFile(filepath.Join(data, current.key, filepath.FromSlash(name)))
		switch {
		case want == nil && err == nil:
			problems = append(problems, fmt.Sprintf("file %s holds %q, want none", name, data))
		case want != nil && err != nil:
			problems = append(problems, fmt.Sprintf("file %s: %v, want %q", name, err, *want))
		case want != nil && string(data) != *want:
			problems = append(problems, fmt.Sprintf("file %s holds %q, want %q", name, data, *want))
		}
	}
	if len(problems) > 0 && len(info.Logs) > 0 {
		lines := []string{"logs:"}
		for _, line := range info.Logs {
			lines = append(lines, "  "+line.Level+": "+line.Message)
		}
		problems = append(problems, strings.Join(lines, "\n"))
	}
	outcome.OK = len(problems) == 0
	outcome.Message = strings.Join(problems, "\n")
	return outcome
}

// Fetch answers a request with the first fake response that matches it.
func (current *runner) Fetch(ctx context.Context, _ *plugin.Plugin, request plugin.HTTPRequest) plugin.HTTPResponse {
	response, found := current.answer(request)
	if !found {
		return plugin.HTTPResponse{Error: plugin.FetchNetwork}
	}
	took := time.Duration(response.DelayMs) * time.Millisecond
	if took > 0 {
		select {
		case <-time.After(took):
		case <-ctx.Done():
			return plugin.HTTPResponse{Error: plugin.FetchTimeout, Took: took}
		}
	}
	if response.Error != "" {
		return plugin.HTTPResponse{Status: response.Status, Error: response.Error, Took: took}
	}
	status := response.Status
	if status == 0 {
		status = 200
	}
	header := map[string]string{}
	for name, value := range response.Headers {
		header[strings.ToLower(name)] = value
	}
	var body []byte
	switch {
	case len(response.JSON) > 0:
		body = response.JSON
		if header["content-type"] == "" {
			header["content-type"] = "application/json"
		}
	case response.Text != nil:
		body = []byte(*response.Text)
	}
	return plugin.HTTPResponse{Status: status, Header: header, Body: body, Took: took}
}

// answer picks the first response that matches the request and has
// answers left, and counts the answer.
func (current *runner) answer(request plugin.HTTPRequest) (fakeResponse, bool) {
	current.mutex.Lock()
	defer current.mutex.Unlock()
	for index, response := range current.responses {
		if response.Times > 0 && current.answered[index] >= response.Times {
			continue
		}
		if response.Method != "" && !strings.EqualFold(response.Method, request.Method) {
			continue
		}
		prefix, wildcard := strings.CutSuffix(response.URL, "*")
		if (!wildcard && response.URL != request.URL) || (wildcard && !strings.HasPrefix(request.URL, prefix)) {
			continue
		}
		if len(response.Body) > 0 && (len(request.JSON) == 0 || !jsonMatches(request.JSON, response.Body)) {
			continue
		}
		current.answered[index]++
		return response, true
	}
	unexpected := fmt.Sprintf("made a request no fixture answers: %s %s", request.Method, request.URL)
	if len(request.JSON) > 0 {
		sent := compact(request.JSON)
		if runes := []rune(sent); len(runes) > 300 {
			sent = string(runes[:300]) + "…"
		}
		unexpected += " " + sent
	}
	current.unexpected = append(current.unexpected, unexpected)
	return fakeResponse{}, false
}

func orUndefined(value json.RawMessage) string {
	if len(value) == 0 {
		return "undefined"
	}
	return compact(value)
}

func compact(value json.RawMessage) string {
	var buffer bytes.Buffer
	if json.Compact(&buffer, value) != nil {
		return string(value)
	}
	return buffer.String()
}

func jsonEqual(a, b json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

// jsonMatches reports whether every member of want appears in got with the
// same value: objects may hold more members, arrays match item by item.
func jsonMatches(got, want json.RawMessage) bool {
	var left, right any
	if json.Unmarshal(got, &left) != nil || json.Unmarshal(want, &right) != nil {
		return false
	}
	return subset(left, right)
}

func subset(got, want any) bool {
	switch wanted := want.(type) {
	case map[string]any:
		object, ok := got.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range wanted {
			if !subset(object[key], value) {
				return false
			}
		}
		return true
	case []any:
		array, ok := got.([]any)
		if !ok || len(array) != len(wanted) {
			return false
		}
		for index := range wanted {
			if !subset(array[index], wanted[index]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(got, want)
}

// metaValues answers astrlink.meta.values with a case's values.
type metaValues map[string][]plugin.ValueStat

func (values metaValues) ValueStats(_ context.Context, key string, limit int) ([]plugin.ValueStat, error) {
	stats := values[key]
	return stats[:min(len(stats), limit)], nil
}

type settingsFunc func() map[string]plugin.Settings

func (read settingsFunc) PluginSettings(context.Context) (map[string]plugin.Settings, error) {
	return read(), nil
}

// memoryStore is astrlink.store for a case.
type memoryStore struct {
	mutex  sync.Mutex
	values map[string]json.RawMessage
}

type storeFunc func() *memoryStore

func (current storeFunc) Get(_ context.Context, _, key string) (json.RawMessage, bool, error) {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	value, found := store.values[key]
	return value, found, nil
}

func (current storeFunc) Set(_ context.Context, _, key string, value json.RawMessage) error {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.values[key] = value
	return nil
}

func (current storeFunc) Delete(_ context.Context, _, key string) error {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.values, key)
	return nil
}

func (current storeFunc) Keys(_ context.Context, _, prefix string) ([]string, error) {
	store := current()
	store.mutex.Lock()
	defer store.mutex.Unlock()
	var keys []string
	for key := range store.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
