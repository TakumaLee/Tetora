package main

// tool_imagegen.go provides compatibility aliases for tools.ImageGenLimiter.
// The handler implementations are in internal/tools/imagegen.go.

import (
	"context"
	"encoding/json"

	"tetora/internal/tools"
)

// Type alias for backwards compat (used by App struct, tests).
type imageGenLimiter = tools.ImageGenLimiter

// globalImageGenLimiter is the default limiter instance.
var globalImageGenLimiter = &tools.ImageGenLimiter{}

// estimateImageCost forwards to tools.EstimateImageCost for test compat.
var estimateImageCost = tools.EstimateImageCost

// imageGenBaseURL forwards to tools.ImageGenBaseURL for test overrides.
var imageGenBaseURL = tools.ImageGenBaseURL

// toolImageGenerate wraps internal/tools image handler for test compat.
func toolImageGenerate(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	// Set the shared base URL before calling.
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateHandler(deps)
	return handler(ctx, cfg, input)
}

// toolImageGenerateStatus wraps internal/tools status handler for test compat.
func toolImageGenerateStatus(ctx context.Context, cfg *Config, _ json.RawMessage) (string, error) {
	tools.ImageGenBaseURL = imageGenBaseURL
	deps := tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			return globalImageGenLimiter
		},
	}
	handler := tools.MakeImageGenerateStatusHandler(deps)
	return handler(ctx, cfg, nil)
}
