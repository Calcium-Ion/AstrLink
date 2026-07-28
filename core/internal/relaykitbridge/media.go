package relaykitbridge

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const maxMediaBytes = 20 << 20

func secureMediaResolver() relayconvert.MediaResolver {
	return relayconvert.MediaResolver{
		GetBase64Data:        resolveMediaURL,
		DecodeBase64FileData: decodeBase64FileData,
	}
}

func resolveMediaURL(ctx context.Context, source types.FileSource, _ ...string) (string, string, error) {
	if source == nil {
		return "", "", errors.New("media source is required")
	}
	if !source.IsURL() {
		return decodeBase64FileData(source.GetRawData())
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	requestURL, err := checkedMediaURL(ctx, source.GetRawData())
	if err != nil {
		return "", "", err
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				if err := validateMediaHost(ctx, host); err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many media redirects")
			}
			_, err := checkedMediaURL(req.Context(), req.URL.String())
			return err
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Referer", "")
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("fetch media: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("fetch media: unexpected HTTP status %d", response.StatusCode)
	}
	if response.ContentLength > maxMediaBytes {
		return "", "", errors.New("media exceeds 20 MiB limit")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxMediaBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read media: %w", err)
	}
	if len(data) > maxMediaBytes {
		return "", "", errors.New("media exceeds 20 MiB limit")
	}
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if !allowedMediaMIME(mimeType) {
		return "", "", fmt.Errorf("unsupported media MIME type %q", mimeType)
	}
	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}

func decodeBase64FileData(raw string) (string, string, error) {
	mimeType := ""
	data := strings.TrimSpace(raw)
	if strings.HasPrefix(data, "data:") {
		header, payload, ok := strings.Cut(data, ",")
		if !ok || !strings.HasSuffix(strings.ToLower(header), ";base64") {
			return "", "", errors.New("invalid base64 data URL")
		}
		mimeType = strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:")
		data = payload
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", "", fmt.Errorf("decode media base64: %w", err)
	}
	if len(decoded) > maxMediaBytes {
		return "", "", errors.New("media exceeds 20 MiB limit")
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(decoded)
	}
	mimeType = strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	if !allowedMediaMIME(mimeType) {
		return "", "", fmt.Errorf("unsupported media MIME type %q", mimeType)
	}
	return base64.StdEncoding.EncodeToString(decoded), mimeType, nil
}

func checkedMediaURL(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("media URL must be an HTTPS URL without credentials")
	}
	if err := validateMediaHost(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateMediaHost(ctx context.Context, host string) error {
	if address, err := netip.ParseAddr(host); err == nil {
		if forbiddenMediaAddress(address) {
			return fmt.Errorf("media URL resolves to forbidden address")
		}
		return nil
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve media host: %w", err)
	}
	if len(addresses) == 0 {
		return errors.New("media host has no addresses")
	}
	for _, address := range addresses {
		if forbiddenMediaAddress(address.Unmap()) {
			return errors.New("media URL resolves to forbidden address")
		}
	}
	return nil
}

func forbiddenMediaAddress(address netip.Addr) bool {
	return !address.IsValid() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified()
}

func allowedMediaMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "audio/") || mimeType == "application/pdf"
}
