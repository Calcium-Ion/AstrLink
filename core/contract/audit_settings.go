package contract

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

const (
	DefaultRequestBodyMaxBytes     = 1_048_576
	DefaultResponseContentMaxBytes = 4_194_304
	DefaultMetadataRetentionDays   = 30
	DefaultContentRetentionDays    = 7

	MinRequestBodyMaxBytes     = 1024
	MaxRequestBodyMaxBytes     = 16_777_216
	MinResponseContentMaxBytes = 1024
	MaxResponseContentMaxBytes = 67_108_864
	MinMetadataRetentionDays   = 1
	MaxMetadataRetentionDays   = 3650
	MinContentRetentionDays    = 1
	MaxContentRetentionDays    = 365
	MaxExtensionsProperties    = 32
)

var extensionNamePattern = regexp.MustCompile(`^x-[a-z0-9][a-z0-9._-]{0,62}$`)

// AuditSettings is the privileged global body-audit configuration document.
type AuditSettings struct {
	RequestBodyEnabled      bool           `json:"request_body_enabled"`
	ResponseContentEnabled  bool           `json:"response_content_enabled"`
	RequestBodyMaxBytes     int            `json:"request_body_max_bytes"`
	ResponseContentMaxBytes int            `json:"response_content_max_bytes"`
	MetadataRetentionDays   int            `json:"metadata_retention_days"`
	ContentRetentionDays    int            `json:"content_retention_days"`
	Extensions              map[string]any `json:"extensions,omitempty"`
}

// DefaultAuditSettings returns the frozen install/upgrade defaults.
func DefaultAuditSettings() AuditSettings {
	return AuditSettings{
		RequestBodyEnabled:      false,
		ResponseContentEnabled:  false,
		RequestBodyMaxBytes:     DefaultRequestBodyMaxBytes,
		ResponseContentMaxBytes: DefaultResponseContentMaxBytes,
		MetadataRetentionDays:   DefaultMetadataRetentionDays,
		ContentRetentionDays:    DefaultContentRetentionDays,
	}
}

func (settings AuditSettings) Validate() error {
	if settings.RequestBodyMaxBytes < MinRequestBodyMaxBytes ||
		settings.RequestBodyMaxBytes > MaxRequestBodyMaxBytes {
		return fmt.Errorf("request_body_max_bytes must be between %d and %d",
			MinRequestBodyMaxBytes, MaxRequestBodyMaxBytes)
	}
	if settings.ResponseContentMaxBytes < MinResponseContentMaxBytes ||
		settings.ResponseContentMaxBytes > MaxResponseContentMaxBytes {
		return fmt.Errorf("response_content_max_bytes must be between %d and %d",
			MinResponseContentMaxBytes, MaxResponseContentMaxBytes)
	}
	if settings.MetadataRetentionDays < MinMetadataRetentionDays ||
		settings.MetadataRetentionDays > MaxMetadataRetentionDays {
		return fmt.Errorf("metadata_retention_days must be between %d and %d",
			MinMetadataRetentionDays, MaxMetadataRetentionDays)
	}
	if settings.ContentRetentionDays < MinContentRetentionDays ||
		settings.ContentRetentionDays > MaxContentRetentionDays {
		return fmt.Errorf("content_retention_days must be between %d and %d",
			MinContentRetentionDays, MaxContentRetentionDays)
	}
	if err := ValidateExtensions(settings.Extensions); err != nil {
		return err
	}
	return nil
}

// AuditSettingsPatch is the merge-patch body for privileged audit settings.
// audit_risk_acknowledged is writeOnly and must never be persisted or echoed.
type AuditSettingsPatch struct {
	RequestBodyEnabled      *bool          `json:"request_body_enabled,omitempty"`
	ResponseContentEnabled  *bool          `json:"response_content_enabled,omitempty"`
	RequestBodyMaxBytes     *int           `json:"request_body_max_bytes,omitempty"`
	ResponseContentMaxBytes *int           `json:"response_content_max_bytes,omitempty"`
	MetadataRetentionDays   *int           `json:"metadata_retention_days,omitempty"`
	ContentRetentionDays    *int           `json:"content_retention_days,omitempty"`
	AuditRiskAcknowledged   *bool          `json:"audit_risk_acknowledged,omitempty"`
	Extensions              map[string]any `json:"extensions,omitempty"`
	ClearExtensions         bool           `json:"-"`
}

func (patch AuditSettingsPatch) Validate() error {
	present := 0
	if patch.RequestBodyEnabled != nil {
		present++
	}
	if patch.ResponseContentEnabled != nil {
		present++
	}
	if patch.RequestBodyMaxBytes != nil {
		present++
		if *patch.RequestBodyMaxBytes < MinRequestBodyMaxBytes ||
			*patch.RequestBodyMaxBytes > MaxRequestBodyMaxBytes {
			return fmt.Errorf("request_body_max_bytes must be between %d and %d",
				MinRequestBodyMaxBytes, MaxRequestBodyMaxBytes)
		}
	}
	if patch.ResponseContentMaxBytes != nil {
		present++
		if *patch.ResponseContentMaxBytes < MinResponseContentMaxBytes ||
			*patch.ResponseContentMaxBytes > MaxResponseContentMaxBytes {
			return fmt.Errorf("response_content_max_bytes must be between %d and %d",
				MinResponseContentMaxBytes, MaxResponseContentMaxBytes)
		}
	}
	if patch.MetadataRetentionDays != nil {
		present++
		if *patch.MetadataRetentionDays < MinMetadataRetentionDays ||
			*patch.MetadataRetentionDays > MaxMetadataRetentionDays {
			return fmt.Errorf("metadata_retention_days must be between %d and %d",
				MinMetadataRetentionDays, MaxMetadataRetentionDays)
		}
	}
	if patch.ContentRetentionDays != nil {
		present++
		if *patch.ContentRetentionDays < MinContentRetentionDays ||
			*patch.ContentRetentionDays > MaxContentRetentionDays {
			return fmt.Errorf("content_retention_days must be between %d and %d",
				MinContentRetentionDays, MaxContentRetentionDays)
		}
	}
	if patch.AuditRiskAcknowledged != nil {
		present++
	}
	if patch.Extensions != nil || patch.ClearExtensions {
		present++
		if patch.Extensions != nil {
			if err := ValidateExtensions(patch.Extensions); err != nil {
				return err
			}
		}
	}
	if present == 0 {
		return fmt.Errorf("audit settings patch must contain at least one property")
	}
	return nil
}

// EnablesCapture reports whether the patch turns either capture switch on.
func (patch AuditSettingsPatch) EnablesCapture() bool {
	return (patch.RequestBodyEnabled != nil && *patch.RequestBodyEnabled) ||
		(patch.ResponseContentEnabled != nil && *patch.ResponseContentEnabled)
}

func (patch AuditSettingsPatch) Acknowledged() bool {
	return patch.AuditRiskAcknowledged != nil && *patch.AuditRiskAcknowledged
}

// AuditContent is the decrypted privileged audit payload for one request.
type AuditContent struct {
	RequestID       RequestID         `json:"request_id"`
	RequestBody     *AuditContentPart `json:"request_body"`
	ResponseContent *AuditContentPart `json:"response_content"`
}

func (content AuditContent) Validate() error {
	if err := content.RequestID.Validate(); err != nil {
		return err
	}
	if content.RequestBody != nil {
		if err := content.RequestBody.Validate(); err != nil {
			return fmt.Errorf("request_body: %w", err)
		}
	}
	if content.ResponseContent != nil {
		if err := content.ResponseContent.Validate(); err != nil {
			return fmt.Errorf("response_content: %w", err)
		}
	}
	return nil
}

// AuditContentPart is one decrypted capture direction.
type AuditContentPart struct {
	MediaType     string `json:"media_type"`
	Content       string `json:"content"`
	Truncated     bool   `json:"truncated"`
	CapturedBytes int    `json:"captured_bytes"`
}

func (part AuditContentPart) Validate() error {
	if part.MediaType == "" || utf8.RuneCountInString(part.MediaType) > 128 {
		return fmt.Errorf("media_type must contain 1 to 128 characters")
	}
	if part.CapturedBytes < 0 {
		return fmt.Errorf("captured_bytes must be non-negative")
	}
	return nil
}

// ValidateExtensions enforces the frozen Extensions object constraints.
func ValidateExtensions(extensions map[string]any) error {
	if extensions == nil {
		return nil
	}
	if len(extensions) > MaxExtensionsProperties {
		return fmt.Errorf("extensions must contain at most %d properties", MaxExtensionsProperties)
	}
	for name := range extensions {
		if !extensionNamePattern.MatchString(name) {
			return fmt.Errorf("extension name %q is invalid", name)
		}
	}
	return nil
}
