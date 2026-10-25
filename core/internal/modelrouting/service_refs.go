package modelrouting

import (
	"context"
	"fmt"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// ValidateServiceReferences checks the complete service/model closure for a
// settings snapshot. It intentionally does not inspect credentials or URLs.
func ValidateServiceReferences(ctx context.Context, services storage.ServiceStore, groups []contract.ModelRoutingGroup) error {
	if services == nil {
		if len(groups) == 0 {
			return nil
		}
		return fmt.Errorf("service store is required to validate model routing members")
	}
	byID := make(map[contract.ServiceID]contract.Service)
	cursor := ""
	for {
		page, err := services.ListServices(ctx, storage.ServiceListOptions{Limit: 200, Cursor: cursor})
		if err != nil {
			return err
		}
		for _, record := range page.Items {
			byID[record.Service.ID] = record.Service
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return fmt.Errorf("service listing cursor did not advance")
		}
		cursor = page.NextCursor
	}
	for _, group := range groups {
		for _, member := range group.Members {
			service, ok := byID[member.ServiceID]
			if !ok {
				return fmt.Errorf("group %q member %q references missing service %q", group.ID, member.ID, member.ServiceID)
			}
			if !service.Enabled {
				return fmt.Errorf("group %q member %q references disabled service %q", group.ID, member.ID, member.ServiceID)
			}
			found := false
			for _, model := range service.Models {
				if model == member.Model {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("group %q member %q references unlisted model %q on service %q", group.ID, member.ID, member.Model, member.ServiceID)
			}
		}
	}
	return nil
}
