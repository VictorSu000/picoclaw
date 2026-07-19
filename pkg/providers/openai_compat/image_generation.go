package openai_compat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/h2non/filetype"

	"github.com/sipeed/picoclaw/pkg/providers/common"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
	"github.com/sipeed/picoclaw/pkg/safehttp"
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

// GenerateImage calls an OpenAI-compatible image endpoint. Requests without
// source images use /images/generations; source images switch the request to
// multipart /images/edits. Responses may contain inline base64 or temporary
// URLs and are always returned as decoded, size-bounded image bytes.
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
	if len(request.InputImages) > 4 {
		return nil, fmt.Errorf("image editing accepts at most 4 input images")
	}
	if request.Mask != nil && len(request.InputImages) == 0 {
		return nil, fmt.Errorf("image editing mask requires at least one input image")
	}
	for index, input := range request.InputImages {
		if err := validateImageGenerationInput(input, maxImageSize); err != nil {
			return nil, fmt.Errorf("validate input image %d: %w", index+1, err)
		}
	}
	if request.Mask != nil {
		if err := validateImageGenerationInput(*request.Mask, maxImageSize); err != nil {
			return nil, fmt.Errorf("validate image mask: %w", err)
		}
		contentType, _, _ := detectGeneratedImageType(request.Mask.Data)
		if contentType != "image/png" {
			return nil, fmt.Errorf("image mask must be a PNG image")
		}
	}

	fields := map[string]any{
		"model":  normalizeModel(strings.TrimSpace(model), p.apiBase),
		"prompt": request.Prompt,
		"n":      count,
	}
	if request.Size != "" {
		fields["size"] = request.Size
	}
	if request.Quality != "" {
		fields["quality"] = request.Quality
	}
	if request.OutputFormat != "" {
		fields["output_format"] = request.OutputFormat
	}
	if request.Background != "" {
		fields["background"] = request.Background
	}
	if request.InputFidelity != "" && len(request.InputImages) > 0 {
		fields["input_fidelity"] = request.InputFidelity
	}
	// Dedicated image model entries may use extra_body for compatible
	// provider-specific options such as response_format. The endpoint model,
	// prompt, requested image count, and multipart file fields cannot be
	// overridden.
	for key, value := range p.extraBody {
		switch key {
		case "model", "prompt", "n", "image", "image[]", "mask":
			continue
		default:
			fields[key] = value
		}
	}

	operation := "generation"
	endpoint := strings.TrimRight(p.apiBase, "/") + "/images/generations"
	var req *http.Request
	var err error
	if len(request.InputImages) > 0 {
		operation = "edit"
		endpoint = strings.TrimRight(p.apiBase, "/") + "/images/edits"
		req, err = newImageEditHTTPRequest(ctx, endpoint, fields, request.InputImages, request.Mask)
	} else {
		req, err = newImageGenerationHTTPRequest(ctx, endpoint, fields)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create image %s request: %w", operation, err)
	}
	req.Header.Set("Accept", "application/json")
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	contentType := req.Header.Get("Content-Type")
	p.applyCustomHeaders(req)
	// Multipart boundaries are generated per request and must not be replaced
	// by a configured static Content-Type header.
	req.Header.Set("Content-Type", contentType)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send image %s request: %w", operation, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}

	responseLimit := imageJSONResponseLimit(maxImageSize, count)
	raw, err := readLimitedBytes(resp.Body, responseLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to read image %s response: %w", operation, err)
	}
	if common.LooksLikeHTML(raw, resp.Header.Get("Content-Type")) {
		return nil, common.WrapHTMLResponseError(resp.StatusCode, raw, resp.Header.Get("Content-Type"), p.apiBase)
	}

	return p.parseImageGenerationResponse(ctx, raw, operation, count, maxImageSize)
}

func newImageGenerationHTTPRequest(
	ctx context.Context,
	endpoint string,
	fields map[string]any,
) (*http.Request, error) {
	jsonData, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func newImageEditHTTPRequest(
	ctx context.Context,
	endpoint string,
	fields map[string]any,
	inputImages []protocoltypes.ImageGenerationInput,
	mask *protocoltypes.ImageGenerationInput,
) (*http.Request, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		encoded, err := encodeImageEditField(value)
		if err != nil {
			return nil, fmt.Errorf("encode multipart field %q: %w", key, err)
		}
		if err := writer.WriteField(key, encoded); err != nil {
			return nil, fmt.Errorf("write multipart field %q: %w", key, err)
		}
	}
	imageField := "image"
	if len(inputImages) > 1 {
		imageField = "image[]"
	}
	for index, input := range inputImages {
		if err := writeImageEditFile(writer, imageField, input, fmt.Sprintf("input-image-%d", index+1)); err != nil {
			return nil, err
		}
	}
	if mask != nil {
		if err := writeImageEditFile(writer, "mask", *mask, "mask"); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("finalize multipart body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, nil
}

func encodeImageEditField(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	case fmt.Stringer:
		return typed.String(), nil
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(encoded), nil
	}
}

func writeImageEditFile(
	writer *multipart.Writer,
	field string,
	input protocoltypes.ImageGenerationInput,
	fallbackBase string,
) error {
	contentType, extension, err := detectGeneratedImageType(input.Data)
	if err != nil {
		return fmt.Errorf("validate multipart file %q: %w", field, err)
	}
	filename := filepath.Base(strings.TrimSpace(input.Filename))
	if filename == "" || filename == "." {
		filename = fallbackBase + "." + extension
	} else if !imageFilenameMatchesType(filename, contentType) {
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		if base == "" || base == "." {
			base = fallbackBase
		}
		filename = base + "." + extension
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
		"name":     field,
		"filename": filename,
	}))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create multipart file %q: %w", field, err)
	}
	if _, err := part.Write(input.Data); err != nil {
		return fmt.Errorf("write multipart file %q: %w", field, err)
	}
	return nil
}

func imageFilenameMatchesType(filename, contentType string) bool {
	extension := strings.ToLower(filepath.Ext(filename))
	switch contentType {
	case "image/png":
		return extension == ".png"
	case "image/jpeg":
		return extension == ".jpg" || extension == ".jpeg"
	case "image/webp":
		return extension == ".webp"
	default:
		return false
	}
}

func validateImageGenerationInput(input protocoltypes.ImageGenerationInput, maxImageSize int) error {
	if len(input.Data) == 0 {
		return fmt.Errorf("image is empty")
	}
	if len(input.Data) > maxImageSize {
		return fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}
	_, _, err := detectGeneratedImageType(input.Data)
	return err
}

func (p *Provider) parseImageGenerationResponse(
	ctx context.Context,
	raw []byte,
	operation string,
	count int,
	maxImageSize int,
) (*protocoltypes.ImageGenerationResponse, error) {
	var payload imageGenerationWireResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse image %s response: %w", operation, err)
	}
	if len(payload.Data) == 0 {
		return nil, fmt.Errorf("image %s response contained no images", operation)
	}
	if len(payload.Data) > count {
		return nil, fmt.Errorf(
			"image %s response contained %d images, requested at most %d",
			operation,
			len(payload.Data),
			count,
		)
	}

	images := make([]protocoltypes.GeneratedImage, 0, len(payload.Data))
	for index, item := range payload.Data {
		var data []byte
		var err error
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
	if safehttp.IsObviousPrivateHost(parsed.Hostname(), nil, func() bool {
		return allowSamePrivateHost
	}) {
		return nil, fmt.Errorf("generated image URL targets a private or local network host")
	}
	client, err := safehttp.CreateSafeHTTPClient(safehttp.SafeHTTPClientOptions{
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
	safehttp.AllowConfiguredProxyFirstHop(req, client.Transport)
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
	return safehttp.IsObviousPrivateHost(base.Hostname(), nil, nil)
}
