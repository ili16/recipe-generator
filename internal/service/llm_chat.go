package service

import (
	"context"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/openai/openai-go"
)

// chatCompletion runs one free-form (no JSON schema) chat completion — used by the
// small/cheap-tier calls (suggest, suggest-prefs, cooking-chat, tag classification, the
// on-topic judge) that just need plain text back.
func chatCompletion(ctx context.Context, systemPrompt, userPrompt, model, callType string) (string, error) {
	result, _, err := llm.Call[string](ctx, llm.Spec{
		Model:    openai.ChatModel(model),
		CallType: callType,
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(userPrompt),
		},
	})
	return result, err
}
