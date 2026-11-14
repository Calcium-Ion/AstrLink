package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func TestImageCompressionTotalsAddUpAndOutliveRecords(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "images.db"))
	defer store.Close()

	start, err := store.GetImageCompressionTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if start.Since.IsZero() || start.Requests != 0 || start.Images != 0 || start.OriginalBytes != 0 || start.SentBytes != 0 {
		t.Fatalf("fresh totals = %+v", start)
	}
	for _, count := range []storage.ImageCompressionCount{
		{Images: 2, Compressed: 1, OriginalBytes: 75301, SentBytes: 37406},
		// Nothing got smaller, so it went as it was and saved nothing.
		{Images: 1, Compressed: 0, OriginalBytes: 900, SentBytes: 900},
	} {
		if err := store.AddImageCompression(ctx, count); err != nil {
			t.Fatal(err)
		}
	}
	for _, bad := range []storage.ImageCompressionCount{
		{},
		{Images: 1, Compressed: 2, OriginalBytes: 10, SentBytes: 5},
		{Images: 1, Compressed: 1, OriginalBytes: 10, SentBytes: 11},
		{Images: 1, OriginalBytes: 10, SentBytes: -1},
	} {
		if err := store.AddImageCompression(ctx, bad); !errors.Is(err, storage.ErrInvalidArgument) {
			t.Fatalf("AddImageCompression(%+v) = %v", bad, err)
		}
	}
	want := storage.ImageCompressionTotals{Since: start.Since, Requests: 2, Images: 3, Compressed: 1, OriginalBytes: 76201, SentBytes: 38306}
	if got, err := store.GetImageCompressionTotals(ctx); err != nil || got != want {
		t.Fatalf("totals = %+v, %v; want %+v", got, err, want)
	}

	// Purging every request record keeps the totals.
	if err := store.InsertRequestRecord(ctx, contract.RequestRecord{
		ID: "request_images", StartedAt: time.Now().UTC().Add(-time.Hour), Status: contract.RequestStatusSucceeded,
		InputProtocol: contract.ProtocolOpenAIChat, Audit: contract.NotCapturedAuditSummary(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeRequestRecords(ctx, contract.PurgeRequest{Scope: contract.PurgeScopeAll, Confirm: true}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetImageCompressionTotals(ctx); err != nil || got != want {
		t.Fatalf("after purge totals = %+v, %v; want %+v", got, err, want)
	}
}
