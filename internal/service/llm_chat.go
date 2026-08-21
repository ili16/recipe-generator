package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/openai/openai-go"
)

func goopenAIChatCompletion(ctx context.Context, systemPrompt, userPrompt string, model string) (string, error) {
	client := OpenAIclient()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.SystemMessage(systemPrompt),
		openai.UserMessage(userPrompt),
	}

	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Messages: openai.F(messages),
		Model:    openai.F(model),
	})

	if err != nil {
		return "", fmt.Errorf("chat completion error: %w", err)
	}

	if len(completion.Choices) == 0 {
		return "", errors.New("no completion choices returned")
	}

	return completion.Choices[0].Message.Content, nil
}
