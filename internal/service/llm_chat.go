package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/openai/openai-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

func goopenAIChatCompletion(ctx context.Context, systemPrompt, userPrompt string, model string, callType string) (string, error) {
	client := OpenAIclient()

	slog.DebugContext(ctx, "llm: chat completion start", "model", model, "call_type", callType, "prompt_len", len(userPrompt), "input", userPrompt)
	t := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", model), attribute.String("llm.call_type", callType))
	defer span.End()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userPrompt),
	}

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    model,
	})

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, callType, model, time.Since(t).Milliseconds(), false)
		return "", fmt.Errorf("chat completion error: %w", err)
	}

	slog.DebugContext(ctx, "llm: chat completion done", "model", model, "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens)
	telemetry.RecordLLMCall(ctx, callType, model, time.Since(t).Milliseconds(), true)
	telemetry.RecordLLMUsage(ctx, callType, model, completion.Usage.PromptTokens, completion.Usage.CompletionTokens)

	if len(completion.Choices) == 0 {
		return "", errors.New("no completion choices returned")
	}

	output := completion.Choices[0].Message.Content
	slog.DebugContext(ctx, "llm: chat completion output", "model", model, "call_type", callType, "output", output)
	return output, nil
}
