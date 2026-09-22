package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/internal/transport"
)

// Plan metadata is optional: reuse the usage request's provider headers and
// bound the extra lookup so its failure does not discard a valid quota snapshot.
func readPlanMetadata(client *http.Client, usageRequest *http.Request, endpoint string, target any) bool {
	ctx, cancel := context.WithTimeout(usageRequest.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	request.Header = usageRequest.Header.Clone()
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, err := transport.ReadResponseBody(response, 1<<20)
	return err == nil && json.Unmarshal(body, target) == nil
}
