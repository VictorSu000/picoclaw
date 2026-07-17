package openai_compat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/h2non/filetype"

	"github.com/sipeed/picoclaw/pkg/providers/common"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	defaultMaxGeneratedImageSize = 20 * 1024 * 1024
	imageResponseEnvelopeBytes   = 1024 * 1024
	maxImageDownloadRedirects    = 3
)

type imageGenerationWireResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	Usage map[string]any `json:"usage"`
}

// GenerateImage calls an OpenAI-compatible /images/generations endpoint.
// The method accepts both inline base64 and temporary URL responses and always
// returns decoded, size-bounded image bytes to the caller.
func (p *Provider) GenerateImage(
	ctx context.Context,
	model string,
	request protocoltypes.ImageGenerationRequest,
) (*protocoltypes.ImageGenerationResponse, error) {
	if p == nil || strings.TrimSpace(p.apiBase) == "" {
		return nil, fmt.Errorf("API base not configured")
	}
	if p.httpClient == nil {
		return nil, fmt.Errorf("HTTP client not configured")
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Prompt == "" {
		return nil, fmt.Errorf("image generation prompt is required")
	}

	count := request.Count
	if count <= 0 {
		count = 1
	}
	if count > 4 {
		return nil, fmt.Errorf("image generation count must not exceed 4")
	}
	maxImageSize := request.MaxImageSize
	if maxImageSize <= 0 {
		maxImageSize = defaultMaxGeneratedImageSize
	}

	body := map[string]any{
		"model":  normalizeModel(strings.TrimSpace(model), p.apiBase),
		"prompt": request.Prompt,
		"n":      count,
	}
	if request.Size != "" {
		body["size"] = request.Size
	}
	if request.Quality != "" {
		body["quality"] = request.Quality
	}
	if request.OutputFormat != "" {
		body["output_format"] = request.OutputFormat
	}
	if request.Background != "" {
		body["background"] = request.Background
	}
	// Dedicated image model entries may use extra_body for compatible
	// provider-specific options such as response_format. The endpoint model,
	// prompt, and requested image count cannot be overridden.
	for key, value := range p.extraBody {
		switch key {
		case "model", "prompt", "n":
			continue
		default:
			body[key] = value
		}
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal image generation request: %w", err)
	}
	endpoint := strings.TrimRight(p.apiBase, "/") + "/images/generations"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create image generation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	p.applyCustomHeaders(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send image generation request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	responseLimit := imageJSONResponseLimit(maxImageSize, count)
	raw, err := readLimitedBytes(resp.Body, responseLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to read image generation response: %w", err)
	}
	if common.LooksLikeHTML(raw, resp.Header.Get("Content-Type")) {
		return nil, common.WrapHTMLResponseError(resp.StatusCode, raw, resp.Header.Get("Content-Type"), p.apiBase)
	}

	var payload imageGenerationWireResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse image generation response: %w", err)
	}
	if len(payload.Data) == 0 {
		return nil, fmt.Errorf("image generation response contained no images")
	}
	if len(payload.Data) > count {
		return nil, fmt.Errorf(
			"image generation response contained %d images, requested at most %d",
			len(payload.Data),
			count,
		)
	}

	images := make([]protocoltypes.GeneratedImage, 0, len(payload.Data))
	for index, item := range payload.Data {
		var data []byte
		switch {
		case strings.TrimSpace(item.B64JSON) != "":
			data, err = decodeImageBase64(item.B64JSON, maxImageSize)
		case strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.URL)), "data:image/"):
			data, err = decodeImageBase64(item.URL, maxImageSize)
		case strings.TrimSpace(item.URL) != "":
			data, err = p.downloadGeneratedImage(ctx, item.URL, maxImageSize)
		default:
			err = fmt.Errorf("response image %d contained neither b64_json nor url", index+1)
		}
		if err != nil {
			return nil, fmt.Errorf("decode generated image %d: %w", index+1, err)
		}

		contentType, extension, err := detectGeneratedImageType(data)
		if err != nil {
			return nil, fmt.Errorf("validate generated image %d: %w", index+1, err)
		}
		images = append(images, protocoltypes.GeneratedImage{
			Data:          data,
			ContentType:   contentType,
			Filename:      fmt.Sprintf("generated-image-%d.%s", index+1, extension),
			RevisedPrompt: strings.TrimSpace(item.RevisedPrompt),
		})
	}

	return &protocoltypes.ImageGenerationResponse{
		Images: images,
		Usage:  payload.Usage,
	}, nil
}

func imageJSONResponseLimit(maxImageSize, count int) int64 {
	// Base64 expands binary data by roughly 4/3. Include a fixed allowance for
	// JSON fields and provider metadata.
	perImage := int64(maxImageSize)*4/3 + 4
	return perImage*int64(count) + imageResponseEnvelopeBytes
}

func readLimitedBytes(reader io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("invalid response size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response exceeded %d bytes", limit)
	}
	return raw, nil
}

func decodeImageBase64(value string, maxImageSize int) ([]byte, error) {
	value = strings.TrimSpace(value)
	if comma := strings.IndexByte(value, ','); strings.HasPrefix(strings.ToLower(value), "data:") && comma >= 0 {
		metadata := strings.ToLower(value[:comma])
		if !strings.HasPrefix(metadata, "data:image/") || !strings.Contains(metadata, ";base64") {
			return nil, fmt.Errorf("unsupported image data URL")
		}
		value = value[comma+1:]
	}
	value = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(value)
	if value == "" {
		return nil, fmt.Errorf("empty base64 image")
	}
	// DecodedLen is an upper bound and may include up to two padding bytes.
	if base64.StdEncoding.DecodedLen(len(value)) > maxImageSize+2 {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 image: %w", err)
	}
	if len(decoded) > maxImageSize {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}
	return decoded, nil
}

func detectGeneratedImageType(data []byte) (contentType string, extension string, err error) {
	kind, err := filetype.Match(data)
	if err != nil || kind == filetype.Unknown {
		return "", "", fmt.Errorf("unsupported or unrecognized image data")
	}
	switch kind.MIME.Value {
	case "image/png":
		return "image/png", "png", nil
	case "image/jpeg":
		return "image/jpeg", "jpg", nil
	case "image/webp":
		return "image/webp", "webp", nil
	default:
		return "", "", fmt.Errorf("unsupported generated image type %q", kind.MIME.Value)
	}
}

func (p *Provider) downloadGeneratedImage(ctx context.Context, rawURL string, maxImageSize int) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid generated image URL")
	}

	allowSamePrivateHost := samePrivateEndpointHost(p.apiBase, rawURL)
	if utils.IsObviousPrivateHost(parsed.Hostname(), nil, func() bool {
		return allowSamePrivateHost
	}) {
		return nil, fmt.Errorf("generated image URL targets a private or local network host")
	}
	client, err := utils.CreateSafeHTTPClient(utils.SafeHTTPClientOptions{
		ProxyURL:     p.proxy,
		Timeout:      imageDownloadTimeout(p.httpClient.Timeout),
		MaxRedirects: maxImageDownloadRedirects,
		AllowPrivateHosts: func() bool {
			return allowSamePrivateHost
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create safe image download client: %w", err)
	}
	if allowSamePrivateHost {
		// A user-configured private image endpoint may return a same-host URL.
		// Do not follow redirects while private hosts are allowed.
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create generated image download request: %w", err)
	}
	utils.AllowConfiguredProxyFirstHop(req, client.Transport)
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download generated image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, rawURL)
	}
	if resp.ContentLength > int64(maxImageSize) {
		return nil, fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}
	return readLimitedBytes(resp.Body, int64(maxImageSize))
}

func imageDownloadTimeout(providerTimeout time.Duration) time.Duration {
	if providerTimeout > 0 {
		return providerTimeout
	}
	return common.DefaultRequestTimeout
}

func samePrivateEndpointHost(apiBase, imageURL string) bool {
	base, baseErr := url.Parse(strings.TrimSpace(apiBase))
	image, imageErr := url.Parse(strings.TrimSpace(imageURL))
	if baseErr != nil || imageErr != nil || !strings.EqualFold(base.Hostname(), image.Hostname()) {
		return false
	}
	return utils.IsObviousPrivateHost(base.Hostname(), nil, nil)
}
