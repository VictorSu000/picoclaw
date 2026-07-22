package integrationtools

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/h2non/filetype"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const (
	maxImageGenerationPromptRunes = 32000
	maxImageGenerationInputImages = 4
	currentImageSelectorPrefix    = "current_image_"
)

var imageGenerationSizePattern = regexp.MustCompile(`^(?:auto|[1-9][0-9]{0,4}x[1-9][0-9]{0,4})$`)

type ImageGenerateTool struct {
	cfg          *config.Config
	primaryModel string
	fallbacks    []string
	maxCount     int
	maxImageSize int
	mediaStore   media.MediaStore
}

func NewImageGenerateTool(
	cfg *config.Config,
	primaryModel string,
	fallbacks []string,
	maxCount int,
	maxImageSize int,
	store media.MediaStore,
) *ImageGenerateTool {
	if maxCount <= 0 || maxCount > 4 {
		maxCount = 4
	}
	if maxImageSize <= 0 {
		maxImageSize = config.DefaultMaxMediaSize
	}
	return &ImageGenerateTool{
		cfg:          cfg,
		primaryModel: strings.TrimSpace(primaryModel),
		fallbacks:    append([]string(nil), fallbacks...),
		maxCount:     maxCount,
		maxImageSize: maxImageSize,
		mediaStore:   store,
	}
}

func (t *ImageGenerateTool) Name() string { return "image_generate" }

func (t *ImageGenerateTool) Description() string {
	return "Generate an image from a text prompt, or edit images already available in the current turn, using the configured image model and send the result to the current chat. " +
		"For an image attached by the user or loaded earlier in this turn, use current_image_N (for example current_image_1) in input_images; do not call load_image or read_file for an already attached image. " +
		"If the user provides only a local image path that has not been loaded, call load_image with that path first, then use the resulting current_image_N selector."
}

func (t *ImageGenerateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{
				"type":        "string",
				"description": "A detailed text description of the image to generate.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional exact model_list alias tagged image_generation. No fallback is used for an explicit override.",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "Number of images to generate.",
				"minimum":     1,
				"maximum":     t.maxCount,
			},
			"size": map[string]any{
				"type":        "string",
				"description": "Optional image size such as 1024x1024, 1536x1024, 1024x1536, or auto.",
			},
			"quality": map[string]any{
				"type":        "string",
				"description": "Optional quality hint.",
				"enum":        []string{"low", "medium", "high", "auto", "standard", "hd"},
			},
			"output_format": map[string]any{
				"type":        "string",
				"description": "Optional output format.",
				"enum":        []string{"png", "jpeg", "webp"},
			},
			"background": map[string]any{
				"type":        "string",
				"description": "Optional background hint. Transparent output requires png or webp.",
				"enum":        []string{"transparent", "opaque", "auto"},
			},
			"input_images": map[string]any{
				"type":        "array",
				"description": "Optional current-turn image selectors for editing/reference generation. Use current_image_1 for the first supported image attached by the user or loaded/produced earlier in this turn, current_image_2 for the second, and so on. Do not call load_image or read_file for an already attached image. If the user provides only a local image path, call load_image on that path first; after it is loaded, use its current_image_N selector. Existing MediaStore references and validated [image:path] values from the current turn are also accepted.",
				"items":       map[string]any{"type": "string"},
				"minItems":    1,
				"maxItems":    maxImageGenerationInputImages,
			},
			"mask": map[string]any{
				"type":        "string",
				"description": "Optional current_image_N selector, MediaStore reference, or validated current-turn [image:path] for a PNG edit mask. If the mask is provided only as a local path, call load_image first. It requires input_images.",
			},
			"input_fidelity": map[string]any{
				"type":        "string",
				"description": "Optional reference-image fidelity hint for editing. It requires input_images.",
				"enum":        []string{"low", "high"},
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Optional display filename for the generated image.",
			},
		},
		"required": []string{"prompt"},
	}
}

func (t *ImageGenerateTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

func (t *ImageGenerateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t.cfg == nil {
		return ErrorResult("image generation configuration is unavailable")
	}
	if t.mediaStore == nil {
		return ErrorResult("media store not configured")
	}
	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" {
		return ErrorResult("no target channel/chat available for generated image delivery")
	}

	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ErrorResult("prompt is required")
	}
	if utf8.RuneCountInString(prompt) > maxImageGenerationPromptRunes {
		return ErrorResult(fmt.Sprintf("prompt exceeds %d characters", maxImageGenerationPromptRunes))
	}

	countValue, err := getInt64Arg(args, "count", 1)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if countValue < 1 || countValue > int64(t.maxCount) {
		return ErrorResult(fmt.Sprintf("count must be between 1 and %d", t.maxCount))
	}

	size, err := imageGenerationStringArg(args, "size")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if size != "" && !imageGenerationSizePattern.MatchString(size) {
		return ErrorResult("size must be auto or WIDTHxHEIGHT")
	}
	quality, err := imageGenerationEnumArg(
		args,
		"quality",
		"low",
		"medium",
		"high",
		"auto",
		"standard",
		"hd",
	)
	if err != nil {
		return ErrorResult(err.Error())
	}
	outputFormat, err := imageGenerationEnumArg(args, "output_format", "png", "jpeg", "webp")
	if err != nil {
		return ErrorResult(err.Error())
	}
	background, err := imageGenerationEnumArg(args, "background", "transparent", "opaque", "auto")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if background == "transparent" && outputFormat == "jpeg" {
		return ErrorResult("transparent background requires png or webp output_format")
	}
	filename, err := imageGenerationStringArg(args, "filename")
	if err != nil {
		return ErrorResult(err.Error())
	}
	modelOverride, err := imageGenerationStringArg(args, "model")
	if err != nil {
		return ErrorResult(err.Error())
	}
	inputImageNames, err := imageGenerationStringSliceArg(args, "input_images")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if len(inputImageNames) > maxImageGenerationInputImages {
		return ErrorResult(fmt.Sprintf("input_images must contain at most %d images", maxImageGenerationInputImages))
	}
	inputImages, err := t.resolveInputImages(ctx, inputImageNames)
	if err != nil {
		return ErrorResult(err.Error())
	}
	maskName, err := imageGenerationStringArg(args, "mask")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if maskName != "" && len(inputImages) == 0 {
		return ErrorResult("mask requires input_images")
	}
	inputFidelity, err := imageGenerationEnumArg(args, "input_fidelity", "low", "high")
	if err != nil {
		return ErrorResult(err.Error())
	}
	if inputFidelity != "" && len(inputImages) == 0 {
		return ErrorResult("input_fidelity requires input_images")
	}
	var mask *providers.ImageGenerationInput
	if maskName != "" {
		resolvedMask, resolveErr := t.resolveInputImage(ctx, maskName)
		if resolveErr != nil {
			return ErrorResult(fmt.Sprintf("invalid mask: %v", resolveErr))
		}
		if resolvedMask.ContentType != "image/png" {
			return ErrorResult("invalid mask: mask must be a PNG image")
		}
		mask = &resolvedMask
	}

	modelNames := t.modelCandidates(modelOverride)
	if len(modelNames) == 0 {
		return ErrorResult("no image generation model configured")
	}
	request := providers.ImageGenerationRequest{
		Prompt:        prompt,
		Count:         int(countValue),
		Size:          size,
		Quality:       quality,
		OutputFormat:  outputFormat,
		Background:    background,
		InputFidelity: inputFidelity,
		MaxImageSize:  t.maxImageSize,
		InputImages:   inputImages,
		Mask:          mask,
	}

	response, selectedModel, attempts, err := t.generateWithFallback(
		ctx,
		modelNames,
		modelOverride != "",
		request,
	)
	if err != nil {
		return ErrorResult(formatImageGenerationFailure(err, attempts)).WithError(err)
	}
	refs, err := t.storeImages(channel, chatID, filename, response.Images)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to store generated image: %v", err)).WithError(err)
	}

	action := "Generated"
	if len(inputImages) > 0 {
		action = "Edited"
	}
	return MediaResult(
		fmt.Sprintf("%s %d image(s) with %s.", action, len(refs), selectedModel),
		refs,
	).WithResponseHandled()
}

func imageGenerationStringSliceArg(args map[string]any, key string) ([]string, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return nil, nil
	}
	switch values := raw.(type) {
	case []string:
		result := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, fmt.Errorf("%s entries must not be empty", key)
			}
			result = append(result, value)
		}
		return result, nil
	case []any:
		result := make([]string, 0, len(values))
		for index, rawValue := range values {
			value, ok := rawValue.(string)
			if !ok || strings.TrimSpace(value) == "" {
				return nil, fmt.Errorf("%s[%d] must be a non-empty string", key, index)
			}
			result = append(result, strings.TrimSpace(value))
		}
		return result, nil
	default:
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
}

func (t *ImageGenerateTool) resolveInputImages(
	ctx context.Context,
	selectors []string,
) ([]providers.ImageGenerationInput, error) {
	if len(selectors) == 0 {
		return nil, nil
	}
	result := make([]providers.ImageGenerationInput, 0, len(selectors))
	seen := make(map[string]struct{}, len(selectors))
	for index, selector := range selectors {
		input, ref, err := t.resolveInputImageWithRef(ctx, selector)
		if err != nil {
			return nil, fmt.Errorf("input_images[%d]: %v", index, err)
		}
		if _, exists := seen[ref]; exists {
			return nil, fmt.Errorf("input_images[%d] duplicates an earlier image", index)
		}
		seen[ref] = struct{}{}
		result = append(result, input)
	}
	return result, nil
}

func (t *ImageGenerateTool) resolveInputImage(
	ctx context.Context,
	selector string,
) (providers.ImageGenerationInput, error) {
	input, _, err := t.resolveInputImageWithRef(ctx, selector)
	return input, err
}

func (t *ImageGenerateTool) resolveInputImageWithRef(
	ctx context.Context,
	selector string,
) (providers.ImageGenerationInput, string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return providers.ImageGenerationInput{}, "", fmt.Errorf("image selector is empty")
	}
	refs := ToolMediaRefs(ctx)
	if len(refs) == 0 {
		return providers.ImageGenerationInput{}, "", fmt.Errorf(
			"image %q is not available in the current turn; attach it or use load_image first",
			selector,
		)
	}

	imageIndex := 0
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if strings.HasPrefix(strings.ToLower(ref), "data:image/") {
			if !supportedInlineImageGenerationType(ref) {
				continue
			}
			imageIndex++
			if selector != ref && selector != currentImageSelector(imageIndex) {
				continue
			}
			input, err := readInlineImageGenerationInput(ref, imageIndex, t.maxImageSize)
			if err != nil {
				return providers.ImageGenerationInput{}, "", err
			}
			return input, ref, nil
		}

		localPath, meta, err := t.mediaStore.ResolveWithMeta(ref)
		if err != nil {
			continue
		}
		if !isSupportedImageGenerationFile(localPath) {
			continue
		}
		imageIndex++
		matches := selector == ref || selector == currentImageSelector(imageIndex)
		if !matches && !strings.HasPrefix(selector, "media://") {
			matches = filepath.Clean(selector) == filepath.Clean(localPath)
			if !matches && strings.HasPrefix(selector, "[image:") && strings.HasSuffix(selector, "]") {
				path := strings.TrimSuffix(strings.TrimPrefix(selector, "[image:"), "]")
				matches = filepath.Clean(path) == filepath.Clean(localPath)
			}
		}
		if !matches {
			continue
		}
		input, err := readImageGenerationInput(localPath, meta, t.maxImageSize)
		if err != nil {
			return providers.ImageGenerationInput{}, "", err
		}
		return input, ref, nil
	}

	return providers.ImageGenerationInput{}, "", fmt.Errorf(
		"image %q is not available in the current turn; use current_image_N for an attached image, or call load_image first for a local path",
		selector,
	)
}

func currentImageSelector(index int) string {
	return fmt.Sprintf("%s%d", currentImageSelectorPrefix, index)
}

func supportedInlineImageGenerationType(dataURL string) bool {
	header, _, found := strings.Cut(dataURL, ",")
	if !found {
		return false
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "data:")))
	mediaType, _, _ = strings.Cut(mediaType, ";")
	return imageExtension(mediaType) != ""
}

func isSupportedImageGenerationFile(localPath string) bool {
	kind, err := filetype.MatchFile(localPath)
	return err == nil && kind != filetype.Unknown && imageExtension(kind.MIME.Value) != ""
}

func readInlineImageGenerationInput(
	dataURL string,
	imageIndex int,
	maxImageSize int,
) (providers.ImageGenerationInput, error) {
	header, value, found := strings.Cut(strings.TrimSpace(dataURL), ",")
	header = strings.ToLower(strings.TrimSpace(header))
	if !found || !strings.HasPrefix(header, "data:image/") || !strings.Contains(header, ";base64") {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d is not a valid base64 image data URL", imageIndex)
	}
	value = strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(value)
	if value == "" {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d is empty", imageIndex)
	}
	if base64.StdEncoding.DecodedLen(len(value)) > maxImageSize+2 {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d exceeds %d bytes", imageIndex, maxImageSize)
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d has invalid base64 data: %w", imageIndex, err)
	}
	if len(data) > maxImageSize {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d exceeds %d bytes", imageIndex, maxImageSize)
	}
	kind, err := filetype.Match(data)
	if err != nil || kind == filetype.Unknown || imageExtension(kind.MIME.Value) == "" {
		return providers.ImageGenerationInput{}, fmt.Errorf("current_image_%d has an unsupported or unrecognized image type", imageIndex)
	}
	return providers.ImageGenerationInput{
		Data:        data,
		ContentType: kind.MIME.Value,
		Filename:    currentImageSelector(imageIndex) + imageExtension(kind.MIME.Value),
	}, nil
}

func readImageGenerationInput(
	localPath string,
	meta media.MediaMeta,
	maxImageSize int,
) (providers.ImageGenerationInput, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return providers.ImageGenerationInput{}, fmt.Errorf("cannot stat image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return providers.ImageGenerationInput{}, fmt.Errorf("image is not a regular file")
	}
	if info.Size() <= 0 {
		return providers.ImageGenerationInput{}, fmt.Errorf("image is empty")
	}
	if info.Size() > int64(maxImageSize) {
		return providers.ImageGenerationInput{}, fmt.Errorf("image exceeds %d bytes", maxImageSize)
	}

	kind, err := filetype.MatchFile(localPath)
	if err != nil || kind == filetype.Unknown {
		return providers.ImageGenerationInput{}, fmt.Errorf("image type is not recognized")
	}
	switch kind.MIME.Value {
	case "image/png", "image/jpeg", "image/webp":
	default:
		return providers.ImageGenerationInput{}, fmt.Errorf("unsupported image type %q", kind.MIME.Value)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return providers.ImageGenerationInput{}, fmt.Errorf("read image: %w", err)
	}
	filename := filepath.Base(strings.TrimSpace(meta.Filename))
	if filename == "" || filename == "." {
		filename = filepath.Base(localPath)
	}
	return providers.ImageGenerationInput{
		Data:        data,
		ContentType: kind.MIME.Value,
		Filename:    filename,
	}, nil
}

func (t *ImageGenerateTool) modelCandidates(override string) []string {
	if override != "" {
		return []string{override}
	}
	queue := append([]string{t.primaryModel}, t.fallbacks...)
	result := make([]string, 0, len(queue))
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		name := strings.TrimSpace(queue[0])
		queue = queue[1:]
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, name)
		if modelCfg, err := t.cfg.GetModelConfig(name); err == nil && modelCfg != nil {
			queue = append(queue, modelCfg.Fallbacks...)
		}
	}
	return result
}

type imageGenerationAttempt struct {
	Model string
	Error error
}

func (t *ImageGenerateTool) generateWithFallback(
	ctx context.Context,
	modelNames []string,
	explicitOverride bool,
	request providers.ImageGenerationRequest,
) (*providers.ImageGenerationResponse, string, []imageGenerationAttempt, error) {
	attempts := make([]imageGenerationAttempt, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelCfg, err := t.cfg.GetModelConfig(modelName)
		if err != nil {
			attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
			if explicitOverride {
				return nil, "", attempts, err
			}
			continue
		}
		if !modelCfg.HasTag(config.ModelTagImageGeneration) {
			err = fmt.Errorf("model must have the %q tag", config.ModelTagImageGeneration)
			attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
			if explicitOverride {
				return nil, "", attempts, err
			}
			continue
		}
		if modelCfg.IsVirtual() {
			err = fmt.Errorf("virtual models cannot be used for image generation")
			attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
			if explicitOverride {
				return nil, "", attempts, err
			}
			continue
		}

		// Tool-schema transforms only affect chat tool definitions. Disable the
		// wrapper here so it cannot hide the provider's optional image-generation
		// capability from the type assertion below.
		providerCfg := *modelCfg
		providerCfg.ToolSchemaTransform = ""
		provider, modelID, err := providers.CreateProviderFromConfig(&providerCfg)
		if err != nil {
			attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
			if explicitOverride {
				return nil, "", attempts, err
			}
			continue
		}
		providerName, _ := providers.ExtractProtocol(modelCfg)
		imageProvider, ok := provider.(providers.ImageGenerationProvider)
		if !ok {
			err = fmt.Errorf("provider %q does not support OpenAI-compatible image generation", providerName)
			attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
			if explicitOverride {
				return nil, "", attempts, err
			}
			continue
		}

		response, err := imageProvider.GenerateImage(ctx, modelID, request)
		if err == nil {
			if response == nil || len(response.Images) == 0 {
				err = fmt.Errorf("provider returned no generated images")
			} else {
				return response, modelName, attempts, nil
			}
		}
		attempts = append(attempts, imageGenerationAttempt{Model: modelName, Error: err})
		if explicitOverride || !canFallbackImageGeneration(err, providerName, modelID) {
			return nil, "", attempts, err
		}
	}
	if len(attempts) == 0 {
		return nil, "", attempts, fmt.Errorf("no usable image generation candidates")
	}
	return nil, "", attempts, attempts[len(attempts)-1].Error
}

func canFallbackImageGeneration(err error, provider, model string) bool {
	classified := providers.ClassifyError(err, provider, model)
	if classified == nil {
		return false
	}
	if classified.Status >= 500 && classified.Status <= 599 {
		return true
	}
	switch classified.Reason {
	case providers.FailoverAuth, providers.FailoverBilling, providers.FailoverRateLimit, providers.FailoverOverloaded:
		return true
	default:
		// A client-side timeout or connection reset can occur after the remote
		// service accepted and billed the generation request. Do not risk an
		// automatic duplicate charge.
		return false
	}
}

func (t *ImageGenerateTool) storeImages(
	channel, chatID, filename string,
	images []providers.GeneratedImage,
) ([]string, error) {
	if len(images) == 0 {
		return nil, fmt.Errorf("no generated images to store")
	}
	if err := os.MkdirAll(media.TempDir(), 0o700); err != nil {
		return nil, err
	}

	scope := fmt.Sprintf("tool:image_generate:%s:%s:%d", channel, chatID, time.Now().UnixNano())
	refs := make([]string, 0, len(images))
	for index, image := range images {
		if len(image.Data) == 0 {
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, fmt.Errorf("generated image %d is empty", index+1)
		}
		if len(image.Data) > t.maxImageSize {
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, fmt.Errorf("generated image %d exceeds %d bytes", index+1, t.maxImageSize)
		}
		extension := imageExtension(image.ContentType)
		if extension == "" {
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, fmt.Errorf("generated image %d has unsupported content type %q", index+1, image.ContentType)
		}

		displayName := generatedImageFilename(filename, image.Filename, extension, index, len(images))
		tmpFile, err := os.CreateTemp(media.TempDir(), "image-generate-*"+extension)
		if err != nil {
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, err
		}
		tmpPath := tmpFile.Name()
		if _, err = tmpFile.Write(image.Data); err != nil {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, err
		}
		if err = tmpFile.Close(); err != nil {
			_ = os.Remove(tmpPath)
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, err
		}

		ref, err := t.mediaStore.Store(tmpPath, media.MediaMeta{
			Filename:      displayName,
			ContentType:   image.ContentType,
			Source:        "tool:image_generate",
			CleanupPolicy: media.CleanupPolicyDeleteOnCleanup,
		}, scope)
		if err != nil {
			_ = os.Remove(tmpPath)
			_ = t.mediaStore.ReleaseAll(scope)
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func imageGenerationStringArg(args map[string]any, key string) (string, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(value), nil
}

func imageGenerationEnumArg(args map[string]any, key string, allowed ...string) (string, error) {
	value, err := imageGenerationStringArg(args, key)
	if err != nil || value == "" {
		return value, err
	}
	value = strings.ToLower(value)
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s", key, strings.Join(allowed, ", "))
}

func imageExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func generatedImageFilename(requested, providerName, extension string, index, total int) string {
	base := strings.TrimSpace(requested)
	if base == "" {
		base = strings.TrimSpace(providerName)
	}
	base = filepath.Base(base)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." {
		base = "generated-image"
	}
	if total > 1 {
		base = fmt.Sprintf("%s-%d", base, index+1)
	}
	return base + extension
}

func formatImageGenerationFailure(err error, attempts []imageGenerationAttempt) string {
	if len(attempts) <= 1 {
		return fmt.Sprintf("image generation failed: %v", err)
	}
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		parts = append(parts, fmt.Sprintf("%s: %v", attempt.Model, attempt.Error))
	}
	return "image generation failed; attempts: " + strings.Join(parts, "; ")
}
