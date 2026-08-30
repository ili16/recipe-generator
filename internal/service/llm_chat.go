package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/openai/openai-go"
)

func goopenAIChatCompletion(ctx context.Context, systemPrompt, userPrompt string, model string) (string, error) {
	client := OpenAIclient()

	slog.Debug("llm: chat completion start", "model", model, "prompt_len", len(userPrompt))
	t := time.Now()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userPrompt),
	}

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: messages,
		Model:    model,
	})

	if err != nil {
		return "", fmt.Errorf("chat completion error: %w", err)
	}

	slog.Debug("llm: chat completion done", "model", model, "dur_ms", time.Since(t).Milliseconds(), "tokens", completion.Usage.TotalTokens)

	if len(completion.Choices) == 0 {
		return "", errors.New("no completion choices returned")
	}

	return completion.Choices[0].Message.Content, nil
}
