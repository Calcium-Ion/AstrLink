package controlapi

import "net/http"

const ImageCompressionTotalsPath = "/control/v1/image-compression-totals"

// getImageCompressionTotals reads what request image compression has done
// since counting began.
func (handler *Handler) getImageCompressionTotals(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	totals, err := handler.requestRecords.GetImageCompressionTotals(request.Context())
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, totals)
}
