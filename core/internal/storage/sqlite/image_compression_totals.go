package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// AddImageCompression adds one forwarded request to the image compression
// totals.
func (store *Store) AddImageCompression(ctx context.Context, count storage.ImageCompressionCount) error {
	if err := count.Validate(); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE image_compression_totals
SET requests = requests + 1, images = images + ?, compressed = compressed + ?,
  original_bytes = original_bytes + ?, sent_bytes = sent_bytes + ?
WHERE id = 1`, count.Images, count.Compressed, count.OriginalBytes, count.SentBytes); err != nil {
		return fmt.Errorf("add image compression totals: %w", err)
	}
	return nil
}

// GetImageCompressionTotals reads the image compression totals.
func (store *Store) GetImageCompressionTotals(ctx context.Context) (storage.ImageCompressionTotals, error) {
	var totals storage.ImageCompressionTotals
	var since string
	if err := store.db.QueryRowContext(ctx, `SELECT since, requests, images, compressed, original_bytes, sent_bytes
FROM image_compression_totals WHERE id = 1`).Scan(
		&since, &totals.Requests, &totals.Images, &totals.Compressed, &totals.OriginalBytes, &totals.SentBytes,
	); err != nil {
		return storage.ImageCompressionTotals{}, fmt.Errorf("read image compression totals: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, since)
	if err != nil {
		return storage.ImageCompressionTotals{}, fmt.Errorf("%w: image compression totals start", storage.ErrInvalidRecord)
	}
	totals.Since = parsed.UTC()
	return totals, nil
}
