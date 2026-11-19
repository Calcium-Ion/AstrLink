package taskplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// Bounds of the poller.
const (
	// pollBatch bounds the due tasks one scan reads, and maxBatchTasks
	// those one batch query asks about.
	pollBatch     = 64
	maxBatchTasks = 50
	// pollWorkers bounds the polls that run at once.
	pollWorkers = 8
)

// pollOutcome is what one poll made of a task: a result to record, or a
// failure that counts against it.
type pollOutcome struct {
	result *taskResult
	data   json.RawMessage
	// fail ends the task at once, as a vendor that no longer knows it.
	fail   string
	reason string
}

// Run polls the tasks that are due until ctx ends.
func (engine *Engine) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-engine.wake:
		}
		engine.pollDue(ctx)
		timer.Reset(engine.pollInterval)
	}
}

// pollDue polls every task that is due, those of a batch plugin together.
func (engine *Engine) pollDue(ctx context.Context) {
	due, err := engine.store.DuePluginTasks(ctx, engine.now(), pollBatch)
	if err != nil {
		if ctx.Err() == nil {
			engine.logf("read the plugin tasks due: %v", err)
		}
		return
	}
	type group struct {
		plugin  string
		service contract.ServiceID
	}
	batches := map[group][]storage.PluginTask{}
	var single []storage.PluginTask
	for _, task := range due {
		target := engine.plugins.Current().Plugin(task.PluginKey)
		if target != nil && target.Task() != nil && target.Task().Batch() {
			key := group{task.PluginKey, task.ServiceID}
			batches[key] = append(batches[key], task)
			continue
		}
		single = append(single, task)
	}
	slots := make(chan struct{}, pollWorkers)
	var wait sync.WaitGroup
	run := func(work func()) {
		slots <- struct{}{}
		wait.Add(1)
		go func() {
			defer func() { <-slots; wait.Done() }()
			work()
		}()
	}
	for _, task := range single {
		run(func() {
			if _, err := engine.poll(ctx, task.ID, true); err != nil && ctx.Err() == nil {
				engine.logf("poll task %s of %s: %v", task.ID, task.PluginKey, err)
			}
		})
	}
	for _, tasks := range batches {
		for start := 0; start < len(tasks); start += maxBatchTasks {
			chunk := tasks[start:min(start+maxBatchTasks, len(tasks))]
			run(func() {
				if err := engine.pollBatch(ctx, chunk); err != nil && ctx.Err() == nil {
					engine.logf("poll %d tasks of %s: %v", len(chunk), chunk[0].PluginKey, err)
				}
			})
		}
	}
	wait.Wait()
}

// Poll asks the vendor about a task once, unless it ended, and returns
// the task as it then is.
func (engine *Engine) Poll(ctx context.Context, id string) (storage.PluginTask, error) {
	return engine.poll(ctx, id, false)
}

// poll polls one task. The poller skips a task another poll holds.
func (engine *Engine) poll(ctx context.Context, id string, background bool) (storage.PluginTask, error) {
	unlock, ok := engine.tryLock(id, background)
	if !ok {
		return storage.PluginTask{}, nil
	}
	defer unlock()
	task, err := engine.store.GetPluginTask(ctx, id)
	if err != nil || task.Terminal() {
		return task, err
	}
	if engine.expired(&task) {
		return task, engine.store.UpdatePluginTask(ctx, task)
	}
	target, meta, service, found, err := engine.taskProvider(ctx, task)
	if err != nil {
		engine.countFailure(&task, describe(err))
		return task, engine.store.UpdatePluginTask(ctx, task)
	}
	call := &exchange{engine: engine, target: target, service: service, meta: meta, credentials: found}
	query := found.query(task)
	var outcome pollOutcome
	if meta.Batch() {
		outcomes, err := call.queryBatch(ctx, []queryContext{query})
		if err != nil {
			outcome.reason = describe(err)
		} else {
			outcome = outcomes[query.TaskID]
		}
	} else {
		outcome = call.query(ctx, query)
	}
	engine.settle(ctx, call, &task, query, outcome)
	return task, engine.store.UpdatePluginTask(ctx, task)
}

// tryLock takes a task's lock: the poller gives up when another poll
// holds it.
func (engine *Engine) tryLock(id string, background bool) (func(), bool) {
	value, _ := engine.locks.LoadOrStore(id, &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	if background {
		if !mutex.TryLock() {
			return nil, false
		}
		return mutex.Unlock, true
	}
	mutex.Lock()
	return mutex.Unlock, true
}

// expired fails a task the vendor did not finish within a day.
func (engine *Engine) expired(task *storage.PluginTask) bool {
	if engine.now().Sub(task.CreatedAt) < taskDeadline {
		return false
	}
	engine.applyResult(task, taskResult{Status: contract.PluginTaskFailure, Reason: "the task did not finish within 24 hours"}, task.Data)
	return true
}

// taskProvider finds the plugin and provider a task runs on, the plugin
// as it now is.
func (engine *Engine) taskProvider(ctx context.Context, task storage.PluginTask) (*plugin.Plugin, *plugin.TaskMeta, contract.Service, credentials, error) {
	target, meta, err := engine.plugin(task.PluginKey)
	if err != nil {
		return nil, nil, contract.Service{}, credentials{}, err
	}
	if engine.services == nil {
		return nil, nil, contract.Service{}, credentials{}, errors.New("providers are unavailable")
	}
	record, err := engine.services.GetService(ctx, task.ServiceID)
	if err != nil {
		return nil, nil, contract.Service{}, credentials{}, fmt.Errorf("the provider of the task is gone: %w", err)
	}
	found, err := engine.credentialsFor(ctx, record.Service, meta)
	if err != nil {
		return nil, nil, contract.Service{}, credentials{}, err
	}
	return target, meta, record.Service, found, nil
}

// query runs one per_task poll: buildQueryRequest, the request, and
// parseTaskResult, the HTTP status read as new-api reads it.
func (call *exchange) query(ctx context.Context, query queryContext) pollOutcome {
	raw, err := call.call(ctx, "buildQueryRequest", query)
	if err != nil {
		return pollOutcome{reason: describe(err)}
	}
	desc, err := parseDescriptor("buildQueryRequest", raw)
	if err != nil {
		return pollOutcome{reason: describe(err)}
	}
	response, err := call.send(ctx, "buildQueryRequest", desc, nil)
	if err != nil {
		return pollOutcome{reason: describe(err)}
	}
	defer response.Body.Close()
	body, _, err := readAnswer(response, maxPollAnswer)
	if err != nil {
		return pollOutcome{reason: describe(err)}
	}
	if outcome, done := classifyPoll(response.StatusCode); done {
		return outcome
	}
	raw, err = call.call(ctx, "parseTaskResult", query, body, hookResponse{Status: response.StatusCode, Headers: firstHeaders(response.Header)})
	if err != nil {
		return pollOutcome{reason: describe(err)}
	}
	var result taskResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return pollOutcome{reason: "parseTaskResult returned something other than a task result"}
	}
	if response.StatusCode >= 300 && !contract.PluginTaskTerminal(result.Status) {
		return pollOutcome{reason: fmt.Sprintf("the vendor answered %d", response.StatusCode)}
	}
	return pollOutcome{result: &result, data: body}
}

// classifyPoll reads the status of a poll's answer before any hook: a task
// the vendor no longer knows fails, a refused key or an overloaded vendor
// counts as a failed poll, any other answer goes to the parse hook.
func classifyPoll(status int) (pollOutcome, bool) {
	switch {
	case status >= 200 && status < 300:
		return pollOutcome{}, false
	case status == http.StatusNotFound || status == http.StatusGone:
		return pollOutcome{fail: fmt.Sprintf("the vendor no longer knows the task (%d)", status)}, true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return pollOutcome{reason: fmt.Sprintf("the vendor refused the provider's key (%d)", status)}, true
	case status == http.StatusTooManyRequests || status >= 500:
		return pollOutcome{reason: fmt.Sprintf("the vendor answered %d", status)}, true
	}
	return pollOutcome{}, false
}

// batchItem is an item of what parseBatchResult returns.
type batchItem = taskResult

// queryBatch runs one batch poll and returns its outcome by upstream task
// id; a task the answer leaves out gets a failed poll.
func (call *exchange) queryBatch(ctx context.Context, queries []queryContext) (map[string]pollOutcome, error) {
	batch := map[string]any{
		"baseUrl": call.credentials.baseURL, "apiKey": call.credentials.apiKey, "authHeader": call.credentials.authHeader,
		"auth": call.credentials.auth, "upstream": call.credentials.upstream, "tasks": queries,
	}
	raw, err := call.call(ctx, "buildBatchQueryRequest", batch, queries)
	if err != nil {
		return nil, err
	}
	desc, err := parseDescriptor("buildBatchQueryRequest", raw)
	if err != nil {
		return nil, err
	}
	response, err := call.send(ctx, "buildBatchQueryRequest", desc, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, _, err := readAnswer(response, maxPollAnswer)
	if err != nil {
		return nil, err
	}
	outcomes := map[string]pollOutcome{}
	if outcome, done := classifyPoll(response.StatusCode); done {
		for _, query := range queries {
			outcomes[query.TaskID] = outcome
		}
		return outcomes, nil
	}
	raw, err = call.call(ctx, "parseBatchResult", batch, body, hookResponse{Status: response.StatusCode, Headers: firstHeaders(response.Header)})
	if err != nil {
		return nil, err
	}
	var items []batchItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errors.New("parseBatchResult returned something other than a list of task results")
	}
	for _, item := range items {
		id := text(item.TaskID)
		if id == "" {
			continue
		}
		if response.StatusCode >= 300 && !contract.PluginTaskTerminal(item.Status) {
			outcomes[id] = pollOutcome{reason: fmt.Sprintf("the vendor answered %d", response.StatusCode)}
			continue
		}
		data := item.Data
		if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			data = nil
		}
		result := item
		outcomes[id] = pollOutcome{result: &result, data: data}
	}
	for _, query := range queries {
		if _, ok := outcomes[query.TaskID]; !ok {
			outcomes[query.TaskID] = pollOutcome{reason: "the vendor's answer left the task out"}
		}
	}
	return outcomes, nil
}

// pollBatch polls the tasks of one batch plugin on one provider together.
func (engine *Engine) pollBatch(ctx context.Context, tasks []storage.PluginTask) error {
	var locked []storage.PluginTask
	for _, task := range tasks {
		if unlock, ok := engine.tryLock(task.ID, true); ok {
			defer unlock()
			current, err := engine.store.GetPluginTask(ctx, task.ID)
			if err == nil && !current.Terminal() {
				locked = append(locked, current)
			}
		}
	}
	if len(locked) == 0 {
		return nil
	}
	var queries []queryContext
	var running []storage.PluginTask
	for index := range locked {
		task := &locked[index]
		if engine.expired(task) {
			if err := engine.store.UpdatePluginTask(ctx, *task); err != nil {
				return err
			}
			continue
		}
		running = append(running, *task)
	}
	if len(running) == 0 {
		return nil
	}
	target, meta, service, found, err := engine.taskProvider(ctx, running[0])
	if err != nil {
		for index := range running {
			engine.countFailure(&running[index], describe(err))
			if err := engine.store.UpdatePluginTask(ctx, running[index]); err != nil {
				return err
			}
		}
		return nil
	}
	call := &exchange{engine: engine, target: target, service: service, meta: meta, credentials: found}
	for _, task := range running {
		queries = append(queries, found.query(task))
	}
	outcomes, err := call.queryBatch(ctx, queries)
	for index := range running {
		outcome := pollOutcome{reason: describe(err)}
		if err == nil {
			outcome = outcomes[queries[index].TaskID]
		}
		engine.settle(ctx, call, &running[index], queries[index], outcome)
		if err := engine.store.UpdatePluginTask(ctx, running[index]); err != nil {
			return err
		}
	}
	return nil
}

// settle records what a poll made of a task.
func (engine *Engine) settle(ctx context.Context, call *exchange, task *storage.PluginTask, query queryContext, outcome pollOutcome) {
	now := engine.now()
	switch {
	case outcome.fail != "":
		engine.applyResult(task, taskResult{Status: contract.PluginTaskFailure, Reason: outcome.fail}, task.Data)
		return
	case outcome.result == nil:
		engine.countFailure(task, outcome.reason)
		return
	}
	result := *outcome.result
	if !slices.Contains(statuses, result.Status) {
		reason := result.Reason
		if reason == "" {
			reason = fmt.Sprintf("the plugin did not recognize the vendor's answer (%q)", result.Status)
		}
		engine.countFailure(task, reason)
		return
	}
	data := outcome.data
	if data == nil {
		data = task.Data
	}
	if state := nullable(result.State); state != nil {
		if len(state) > storage.MaxPluginTaskStateBytes {
			engine.logf("task %s: the plugin's state is larger than 1 MiB and was not kept", task.ID)
		} else {
			task.State = state
		}
	}
	task.PollFailures = 0
	engine.applyResult(task, result, data)
	if task.Status == contract.PluginTaskSuccess && call.hasHook("extractUsageOnComplete") {
		completed := query
		completed.Data, completed.State = jsonOrNull(task.Data), jsonOrNull(task.State)
		if raw, err := call.call(ctx, "extractUsageOnComplete", completed, result, jsonOrNull(data)); err == nil {
			facts := map[string]any{}
			mergeFacts(facts, task.Usage)
			mergeFacts(facts, raw)
			if len(facts) > 0 {
				task.Usage, _ = json.Marshal(facts)
			}
		} else {
			engine.logf("task %s: extractUsageOnComplete: %v", task.ID, err)
		}
	}
	if !task.Terminal() {
		task.NextPollAt = now.Add(pollDelay(now.Sub(task.CreatedAt)))
	}
}

// countFailure counts a failed poll, and fails the task after
// maxPollFailures of them in a row.
func (engine *Engine) countFailure(task *storage.PluginTask, reason string) {
	now := engine.now()
	task.PollFailures++
	task.UpdatedAt = now
	if task.PollFailures >= maxPollFailures {
		if reason == "" {
			reason = "the vendor's answers could not be read"
		}
		engine.applyResult(task, taskResult{Status: contract.PluginTaskFailure, Reason: fmt.Sprintf("polling failed %d times in a row: %s", task.PollFailures, reason)}, task.Data)
		return
	}
	task.NextPollAt = now.Add(pollDelay(now.Sub(task.CreatedAt)))
}

// Await polls a task every interval until it ends, ctx ends or observe
// returns an error, calling observe with the task after each poll.
func (engine *Engine) Await(ctx context.Context, id string, interval time.Duration, observe func(storage.PluginTask) error) (storage.PluginTask, error) {
	for {
		task, err := engine.Poll(ctx, id)
		if err != nil {
			return task, err
		}
		if observe != nil {
			if err := observe(task); err != nil {
				return task, err
			}
		}
		if task.Terminal() {
			return task, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return task, ctx.Err()
		case <-timer.C:
		}
	}
}
