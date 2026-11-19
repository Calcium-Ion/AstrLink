package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/plugin/view"
)

// The observeTurn queue: 256 captures waiting at most, two running at once,
// each with 30 s of network. A capture that finds the queue full is dropped
// and counted; a failed one never touches its request.
const (
	observeQueueSize = 256
	observeWorkers   = 2
	observeTimeout   = 30 * time.Second
)

type observeJob struct {
	plugin *plugin.Plugin
	view   TurnView
	info   plugin.RequestInfo
	// deliver keeps the capture's record with its request.
	deliver func(contract.RequestPluginRecord)
}

type observeQueue struct {
	jobs    chan observeJob
	stop    chan struct{}
	once    sync.Once
	workers sync.WaitGroup
	logf    func(string, ...any)

	mutex   sync.Mutex
	dropped map[string]int
}

func newObserveQueue(logf func(string, ...any)) *observeQueue {
	queue := &observeQueue{
		jobs: make(chan observeJob, observeQueueSize), stop: make(chan struct{}),
		logf: logf, dropped: map[string]int{},
	}
	queue.workers.Add(observeWorkers)
	for range observeWorkers {
		go queue.work()
	}
	return queue
}

// push queues a capture; false when the queue is full or stopped.
func (queue *observeQueue) push(job observeJob) bool {
	select {
	case <-queue.stop:
		return false
	default:
	}
	select {
	case queue.jobs <- job:
		return true
	default:
		queue.mutex.Lock()
		queue.dropped[job.plugin.Key]++
		queue.mutex.Unlock()
		return false
	}
}

// Dropped counts, by plugin, the captures a full queue dropped since Core
// started.
func (host *Host) Dropped() map[string]int {
	if host == nil {
		return nil
	}
	queue := host.observe
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	counts := make(map[string]int, len(queue.dropped))
	for key, count := range queue.dropped {
		counts[key] = count
	}
	return counts
}

func (queue *observeQueue) work() {
	defer queue.workers.Done()
	for {
		select {
		case <-queue.stop:
			return
		case job := <-queue.jobs:
			ctx, cancel := context.WithTimeout(context.Background(), observeTimeout)
			raw, info, err := job.plugin.Call(ctx, plugin.HookObserveTurn, job.view, plugin.CallOptions{
				Request: &job.info, NetworkBudget: observeTimeout,
			})
			cancel()
			if err != nil {
				queue.logf("plugin %s observeTurn: %v", job.plugin.Key, err)
			}
			if record, ok := observeRecord(job.plugin, raw, info, err); ok && job.deliver != nil {
				job.deliver(record)
			}
		}
	}
}

// observeRecord is what a capture reports for its request: the record the
// plugin gave, or the host's account of a call that failed. A capture that
// went well without saying so reports nothing.
func observeRecord(owner *plugin.Plugin, raw json.RawMessage, info plugin.CallInfo, err error) (contract.RequestPluginRecord, bool) {
	record := contract.RequestPluginRecord{
		Plugin: owner.Key, PluginVersion: owner.Meta.Version, Hook: plugin.HookObserveTurn,
		Title: contract.ClampRunes(owner.Meta.Name, contract.MaxPluginRecordTitleRunes), Status: contract.PluginRecordOK,
		ElapsedMs: int(info.Elapsed.Milliseconds()),
		Network: contract.PluginNetworkCount{
			Requests: info.Network.Requests, Denied: info.Network.Denied,
			Errors: info.Network.Errors, WaitMs: int(info.Network.Wait.Milliseconds()),
		},
	}
	if err != nil {
		record.Status, record.SkippedReason = contract.PluginRecordFailed, contract.PluginSkipPluginError
		var failure *plugin.CallError
		if errors.As(err, &failure) && (failure.Reason == plugin.ReasonTimeout || failure.Reason == plugin.ReasonCanceled) {
			record.SkippedReason = contract.PluginSkipTimeout
		}
		return record, true
	}
	var fields struct {
		Record json.RawMessage `json:"record"`
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return record, false
	}
	if json.Unmarshal(trimmed, &fields) != nil {
		record.Status, record.SkippedReason = contract.PluginRecordFailed, skipInvalidResult
		return record, true
	}
	if len(fields.Record) == 0 || bytes.Equal(fields.Record, []byte("null")) {
		return record, false
	}
	given, ok := view.ParseRecord(fields.Record)
	if !ok {
		record.Status, record.SkippedReason = contract.PluginRecordFailed, skipInvalidResult
		return record, true
	}
	record.Title, record.Status, record.Summary = given.Title, given.Status, given.Summary
	if owner.Can(plugin.CapabilityViews) {
		record.View, record.Truncated = given.View, given.Truncated
	}
	return record, true
}

func (queue *observeQueue) close() {
	queue.once.Do(func() { close(queue.stop) })
	queue.workers.Wait()
}
