package pluginhost

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/new-api/pluginkit"
)

// The report hooks hear of each finished request, off the request path: a
// fixed pool of workers runs them from a bounded queue, and a request past
// its bounds is dropped. A report changes nothing for the request.

// Bounds of the report queue.
const (
	reportWorkers    = 8
	reportQueueSize  = 256
	reportQueueBytes = 64 << 20
	reportDropLog    = time.Minute
)

// RequestReport is what report hears of a finished request; the host
// fills in requestPlugins.
type RequestReport struct {
	RequestID       string          `json:"requestId"`
	StartedAt       int64           `json:"startedAt"`
	DurationMs      int64           `json:"durationMs"`
	FirstResponseMs *int64          `json:"firstResponseMs,omitempty"`
	Request         ReportRequest   `json:"request"`
	Response        *ReportResponse `json:"response,omitempty"`
	Usage           map[string]int  `json:"usage,omitempty"`
	Cost            ReportCost      `json:"cost"`
	Error           *ReportError    `json:"error,omitempty"`
	RequestPlugins  []any           `json:"requestPlugins"`
	// Format and UpstreamModel are the last attempt's, for ctx.
	Format        contract.ProtocolID `json:"-"`
	UpstreamModel string              `json:"-"`
	Attempt       int                 `json:"-"`
}

type ReportRequest struct {
	Body  json.RawMessage            `json:"body"`
	Media []pluginkit.TransformMedia `json:"media,omitempty"`
}

type ReportResponse struct {
	Status      int             `json:"status"`
	ContentType string          `json:"contentType"`
	Stream      bool            `json:"stream"`
	Body        json.RawMessage `json:"body,omitempty"`
	Bytes       int64           `json:"bytes"`
	Truncated   bool            `json:"truncated"`
}

type ReportCost struct {
	Quota float64 `json:"quota"`
	USD   float64 `json:"usd"`
}

type ReportError struct {
	Status  int    `json:"status"`
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

type reportJob struct {
	run    *RequestRun
	record RequestReport
	size   int
}

// reportQueue runs the report hooks of finished requests.
type reportQueue struct {
	once    sync.Once
	jobs    chan reportJob
	mutex   sync.Mutex
	bytes   int
	dropped int
	logged  time.Time
}

// ServesReport reports whether an instance of the request hears of it
// when it ends.
func (run *RequestRun) ServesReport() bool { return run.Serves(contract.RequestStageReport) }

// Report queues the request's report for its report hooks. The body may
// hold inline files; the hooks see them as "[media:N]".
func (run *RequestRun) Report(record RequestReport) {
	if !run.ServesReport() {
		return
	}
	hidden, media, err := hideMedia(record.Request.Body)
	if err == nil && len(record.Request.Body) > 0 {
		record.Request.Body = hidden
		record.Request.Media = reportMedia(media)
	}
	if len(record.Request.Body) == 0 {
		record.Request.Body = json.RawMessage("null")
	}
	run.mutex.Lock()
	record.RequestPlugins = make([]any, 0, len(run.steps))
	for _, step := range run.steps {
		record.RequestPlugins = append(record.RequestPlugins, step)
	}
	run.mutex.Unlock()
	queue := &run.host.reports
	queue.once.Do(func() {
		queue.jobs = make(chan reportJob, reportQueueSize)
		for range reportWorkers {
			go queue.work(run.host)
		}
	})
	size := len(record.Request.Body)
	if record.Response != nil {
		size += len(record.Response.Body)
	}
	queue.mutex.Lock()
	if queue.bytes+size > reportQueueBytes {
		queue.drop(run.host)
		queue.mutex.Unlock()
		return
	}
	queue.bytes += size
	queue.mutex.Unlock()
	select {
	case queue.jobs <- reportJob{run: run, record: record, size: size}:
	default:
		queue.mutex.Lock()
		queue.bytes -= size
		queue.drop(run.host)
		queue.mutex.Unlock()
	}
}

func (queue *reportQueue) drop(host *Host) {
	queue.dropped++
	if time.Since(queue.logged) >= reportDropLog {
		host.logf("request plugins: %d reports dropped, the queue was full", queue.dropped)
		queue.dropped, queue.logged = 0, time.Now()
	}
}

func (queue *reportQueue) work(host *Host) {
	for job := range queue.jobs {
		job.run.report(job.record)
		queue.mutex.Lock()
		queue.bytes -= job.size
		queue.mutex.Unlock()
	}
}

// report runs each instance's report hook on one record.
func (run *RequestRun) report(record RequestReport) {
	for _, current := range run.instances {
		if !current.meta.HasStage(contract.RequestStageReport) {
			continue
		}
		tctx := run.transformContext(current, contract.RequestStageReport, RequestFormat(record.Format), RequestAttempt{Index: record.Attempt, UpstreamModel: record.UpstreamModel})
		encoded, err := json.Marshal(record)
		if err == nil {
			_, err = callRequestHook(context.Background(), current, contract.RequestStageReport, nil, func(ctx context.Context, loaded *plugin.NewAPIPlugin) (struct{}, error) {
				return struct{}{}, loaded.CallReport(ctx, tctx, encoded)
			})
		}
		if err != nil {
			run.host.logf("request plugin %s (%s): report failed", current.instance.ID, current.plugin.Key)
		}
	}
}

// reportMedia is how a report describes the inline files of its body.
func reportMedia(media []requestMedia) []pluginkit.TransformMedia {
	view := make([]pluginkit.TransformMedia, 0, len(media))
	for index, item := range media {
		view = append(view, pluginkit.TransformMedia{Index: index, Path: item.path, MimeType: item.mime, Bytes: int(item.bytes), Width: item.width, Height: item.height})
	}
	return view
}
