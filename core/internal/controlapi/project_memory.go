package controlapi

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

// RequestMemoryPath reads what one client's agents keep in one project.
const RequestMemoryPath = "/control/v1/request-memory"

// RequestMemoryEntryPath reads one memory of that view and what happened to
// it.
const RequestMemoryEntryPath = "/control/v1/request-memory/entry"

// maxMemoryDayLag bounds how long before now the reader's day may have
// started; a day is at most 25 hours where clocks change.
const maxMemoryDayLag = 49 * time.Hour

func (handler *Handler) getProjectMemory(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	options, _, err := parseProjectMemoryOptions(request, time.Now(), false)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	memory, err := handler.requestRecords.GetProjectMemory(request.Context(), options)
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, memory)
}

func (handler *Handler) getProjectMemoryEntry(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	options, file, err := parseProjectMemoryOptions(request, time.Now(), true)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	detail, err := handler.requestRecords.GetProjectMemoryEntry(request.Context(), options, file)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(writer, http.StatusNotFound, "not_found", "memory not found")
		return
	}
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, detail)
}

// parseProjectMemoryOptions reads project (a project directory), no_project
// (true for the calls that named none), client_type, day_start (when the
// reader's day began; local midnight without it) and, for one memory, file.
func parseProjectMemoryOptions(request *http.Request, now time.Time, entry bool) (storage.ProjectMemoryOptions, string, error) {
	query := request.URL.Query()
	local := now.Local()
	options := storage.ProjectMemoryOptions{
		Now:      now.UTC(),
		DayStart: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).UTC(),
	}
	file := ""
	for key, values := range query {
		if len(values) != 1 {
			return options, "", fmt.Errorf("query parameter must occur once")
		}
		value := values[0]
		switch key {
		case "project":
			if value == "" || len([]rune(value)) > contract.MaxContextPathRunes {
				return options, "", fmt.Errorf("project must be 1 to %d characters", contract.MaxContextPathRunes)
			}
			options.Project = &value
		case "no_project":
			if value != "true" {
				return options, "", fmt.Errorf("no_project must be true")
			}
			options.NoProject = true
		case "client_type":
			client := contract.ClientType(value)
			if !client.Valid() {
				return options, "", fmt.Errorf("client_type is unknown")
			}
			options.ClientType = &client
		case "day_start":
			start, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return options, "", fmt.Errorf("invalid day_start timestamp")
			}
			if start.After(now) || now.Sub(start) > maxMemoryDayLag {
				return options, "", fmt.Errorf("day_start must be within the last two days")
			}
			options.DayStart = start.UTC()
		case "file":
			if !entry {
				return options, "", fmt.Errorf("unsupported query parameter %q", key)
			}
			if value == "" || len([]rune(value)) > contract.MaxMemoryEntryFileRunes {
				return options, "", fmt.Errorf("file must be 1 to %d characters", contract.MaxMemoryEntryFileRunes)
			}
			file = value
		default:
			return options, "", fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	if options.Project != nil && options.NoProject {
		return options, "", fmt.Errorf("project and no_project cannot both be set")
	}
	if entry && file == "" {
		return options, "", fmt.Errorf("file is required")
	}
	return options, file, nil
}
