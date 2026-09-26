package contract

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"strings"
)

// IntelligenceRequest is constructed only by the authenticated evaluation runner.
// Connection tests retain their own small output and time limits.
type IntelligenceRequest struct {
	ServiceTestRequest
	TimeoutSeconds int      `json:"timeout_seconds"`
	Images         []string `json:"images,omitempty"`
}

func (r IntelligenceRequest) Validate(service Service) error {
	if err := r.ServiceTestRequest.Validate(service); err != nil {
		return err
	}
	if r.TimeoutSeconds < 1 || r.TimeoutSeconds > 300 {
		return fmt.Errorf("invalid generation timeout")
	}
	if len(r.Images) > 2 {
		return fmt.Errorf("at most two images are supported")
	}
	if len(r.Images) > 0 && r.Protocol == ProtocolOpenAICompletions {
		return fmt.Errorf("selected protocol does not accept images")
	}
	for _, data := range r.Images {
		if err := ValidateIntelligencePNG(data); err != nil {
			return err
		}
	}
	return nil
}

func ValidateIntelligencePNG(data string) error {
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(data, prefix) || len(data) > 350000 {
		return fmt.Errorf("image must be a PNG smaller than 256 KiB")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(data, prefix))
	if err != nil || len(raw) > 256*1024 {
		return fmt.Errorf("invalid PNG encoding")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
		return fmt.Errorf("invalid PNG dimensions (maximum 2048 x 2048)")
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		return fmt.Errorf("invalid PNG data")
	}
	return nil
}
