package storage

import (
	"fmt"
	"time"
)

// ImageCompressionTotals adds up what request image compression did to the
// requests it forwarded since counting began. A request counts once,
// however many retries or failovers it took. Sizes are decoded image bytes,
// as in the image_compression event; an image forwarded as it was, because
// its WebP was not smaller, adds the same size to both sums and saves
// nothing. The totals live apart from request records, so deleting or
// purging records does not lower them.
type ImageCompressionTotals struct {
	// Since is when counting began: when the install was upgraded to a
	// version that counts, or created with one.
	Since time.Time `json:"since"`
	// Requests went upstream with at least one inline image.
	Requests int64 `json:"requests"`
	// Images is every inline image of those requests, Compressed the ones
	// sent as a smaller WebP.
	Images     int64 `json:"images"`
	Compressed int64 `json:"compressed"`
	// OriginalBytes is the size of every image as the client sent it,
	// SentBytes as it went upstream.
	OriginalBytes int64 `json:"original_bytes"`
	SentBytes     int64 `json:"sent_bytes"`
}

// ImageCompressionCount is what compression did to one forwarded request.
type ImageCompressionCount struct {
	Images, Compressed       int
	OriginalBytes, SentBytes int64
}

// Validate rejects a count compression cannot produce: an image is only
// replaced by a smaller one.
func (count ImageCompressionCount) Validate() error {
	if count.Images < 1 || count.Compressed < 0 || count.Compressed > count.Images ||
		count.SentBytes < 0 || count.SentBytes > count.OriginalBytes {
		return fmt.Errorf("%w: image compression count %+v", ErrInvalidArgument, count)
	}
	return nil
}
