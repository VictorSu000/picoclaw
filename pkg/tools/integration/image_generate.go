package integrationtools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const maxImageGenerationPromptRunes = 32000

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
	return "Generate images from a text prompt using the configured image generation model and send them to the current chat."
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

	modelNames := t.modelCandidates(modelOverride)
	if len(modelNames) == 0 {
		return ErrorResult("no image generation model configured")
	}
	request := providers.ImageGenerationRequest{
		Prompt:       prompt,
		Count:        int(countValue),
		Size:         size,
		Quality:      quality,
		OutputFormat: outputFormat,
		Background:   background,
		MaxImageSize: t.maxImageSize,
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

	return MediaResult(
		fmt.Sprintf("Generated %d image(s) with %s.", len(refs), selectedModel),
		refs,
	).WithResponseHandled()
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
