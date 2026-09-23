package privacy

import "context"

// InspectionProgress tells a waiting request how far a detector has come. It
// carries counts and sizes only, never content. Bytes count the text sent to
// the model, which is smaller than the request body.
type InspectionProgress struct {
	Segments, CachedSegments  int
	Bytes, CachedBytes        int
	InspectedBytes            int
	Batches, CompletedBatches int
}

type inspectionProgressKey struct{}

// WithInspectionProgress lets a detector report progress on a long request.
func WithInspectionProgress(ctx context.Context, report func(InspectionProgress)) context.Context {
	return context.WithValue(ctx, inspectionProgressKey{}, report)
}

func ReportInspectionProgress(ctx context.Context, progress InspectionProgress) {
	if report, _ := ctx.Value(inspectionProgressKey{}).(func(InspectionProgress)); report != nil {
		report(progress)
	}
}
