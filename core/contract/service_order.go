package contract

// ServiceOrder is the global priority order, including disabled services.
type ServiceOrder struct {
	ServiceIDs []ServiceID `json:"service_ids"`
}
