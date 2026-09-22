package controlapi

import (
	"context"
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
)

type ServiceTester interface {
	Test(context.Context, contract.Service, contract.ServiceTestRequest) contract.ServiceTestResult
}

func (handler *Handler) testService(writer http.ResponseWriter, request *http.Request, id contract.ServiceID) {
	if !requireMediaType(writer, request, "application/json") {
		return
	}
	var input contract.ServiceTestRequest
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	record, err := handler.serviceStore.GetService(request.Context(), id)
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	if err := input.Validate(record.Service); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_service_test", err.Error())
		return
	}
	if handler.serviceTester == nil {
		writeError(writer, http.StatusServiceUnavailable, "service_test_unavailable", "provider testing is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, handler.serviceTester.Test(request.Context(), record.Service, input))
}
