package controlapi

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const maxControlBodyBytes = 1 << 20

type credentialInput struct {
	Secret string `json:"secret"`
}

type serviceAuthInput struct {
	Scheme     *contract.AuthScheme `json:"scheme"`
	HeaderName json.RawMessage      `json:"header_name,omitempty"`
}

type serviceCapabilityInput struct {
	Protocol  *contract.ProtocolID     `json:"protocol"`
	Mode      *contract.CapabilityMode `json:"mode"`
	Streaming *bool                    `json:"streaming"`
}

func (handler *Handler) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "Bearer "
		authorization := request.Header.Get("Authorization")
		provided := []byte("")
		if strings.HasPrefix(authorization, prefix) {
			provided = []byte(strings.TrimPrefix(authorization, prefix))
		}
		if len(provided) != len(handler.controlToken) ||
			subtle.ConstantTimeCompare(provided, handler.controlToken) != 1 {
			writer.Header().Set("WWW-Authenticate", `Bearer realm="astrlink-control"`)
			writeError(
				writer,
				http.StatusUnauthorized,
				"unauthorized",
				"missing or invalid local control token",
			)
			return
		}
		next(writer, request)
	}
}

func decodeServiceCapabilities(raw json.RawMessage) ([]contract.Capability, error) {
	if isJSONNull(raw) {
		return nil, fmt.Errorf("capabilities must be an array")
	}
	var inputs []serviceCapabilityInput
	if err := strictUnmarshal(raw, &inputs); err != nil {
		return nil, fmt.Errorf("decode capabilities: %w", err)
	}
	capabilities := make([]contract.Capability, 0, len(inputs))
	for index, input := range inputs {
		if input.Protocol == nil || input.Mode == nil || input.Streaming == nil {
			return nil, fmt.Errorf(
				"capabilities[%d] must include protocol, mode, and streaming",
				index,
			)
		}
		capabilities = append(capabilities, contract.Capability{
			Protocol:  *input.Protocol,
			Mode:      *input.Mode,
			Streaming: *input.Streaming,
		})
	}
	return capabilities, nil
}

func decodeServiceModels(raw json.RawMessage) ([]string, error) {
	if raw == nil {
		return []string{}, nil
	}
	if isJSONNull(raw) {
		return nil, fmt.Errorf("models must be an array")
	}
	var models []string
	if err := strictUnmarshal(raw, &models); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	return contract.NormalizeServiceModels(models)
}

func decodeServiceAuth(raw json.RawMessage) (contract.ServiceAuth, error) {
	if isJSONNull(raw) {
		return contract.ServiceAuth{}, fmt.Errorf("auth must be an object")
	}
	var input serviceAuthInput
	if err := strictUnmarshal(raw, &input); err != nil {
		return contract.ServiceAuth{}, fmt.Errorf("decode auth: %w", err)
	}
	if input.Scheme == nil {
		return contract.ServiceAuth{}, fmt.Errorf("auth.scheme is required")
	}
	auth := contract.ServiceAuth{Scheme: *input.Scheme}
	if input.HeaderName != nil {
		if isJSONNull(input.HeaderName) {
			return contract.ServiceAuth{}, fmt.Errorf("auth.header_name must be a string")
		}
		if err := strictUnmarshal(input.HeaderName, &auth.HeaderName); err != nil {
			return contract.ServiceAuth{}, fmt.Errorf("decode auth.header_name: %w", err)
		}
	}
	if auth.Scheme == contract.AuthSchemeCustomHeader {
		if input.HeaderName == nil {
			return contract.ServiceAuth{}, fmt.Errorf("custom_header auth requires header_name")
		}
	} else if input.HeaderName != nil {
		return contract.ServiceAuth{}, fmt.Errorf(
			"header_name is only valid for custom_header auth",
		)
	}
	if err := auth.Validate(); err != nil {
		return contract.ServiceAuth{}, err
	}
	return auth, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func requireMediaType(
	writer http.ResponseWriter,
	request *http.Request,
	expected string,
) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != expected {
		writeError(
			writer,
			http.StatusUnsupportedMediaType,
			"unsupported_media_type",
			"request Content-Type is not supported",
		)
		return false
	}
	return true
}

func decodeControlJSON(
	writer http.ResponseWriter,
	request *http.Request,
	target any,
) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, maxControlBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_json",
			"request body is not valid JSON",
		)
		return false
	}
	if err := ensureControlJSONEOF(decoder); err != nil {
		writeError(
			writer,
			http.StatusBadRequest,
			"invalid_json",
			"request body must contain exactly one JSON value",
		)
		return false
	}
	return true
}

func strictUnmarshal(value []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return ensureControlJSONEOF(decoder)
}

func ensureControlJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (handler *Handler) writeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, storage.ErrConflict):
		writeError(
			writer,
			http.StatusConflict,
			"conflict",
			"resource conflicts with current state",
		)
	case errors.Is(err, storage.ErrPrecondition):
		writeError(
			writer,
			http.StatusPreconditionFailed,
			"precondition_failed",
			"If-Match does not match the current resource",
		)
	case errors.Is(err, storage.ErrInvalidCursor):
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "cursor is invalid")
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(
			writer,
			http.StatusUnprocessableEntity,
			"invalid_resource",
			"resource violates the storage contract",
		)
	case errors.Is(err, storage.ErrInvalidRecord):
		writeError(
			writer,
			http.StatusInternalServerError,
			"persisted_state_invalid",
			"persisted resource state failed validation",
		)
	default:
		writeError(
			writer,
			http.StatusInternalServerError,
			"storage_unavailable",
			"persistent storage is unavailable",
		)
	}
}

func randomServiceID() (contract.ServiceID, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return contract.ServiceID("service_" + hex.EncodeToString(random[:])), nil
}
