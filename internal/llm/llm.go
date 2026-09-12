// Package llm is the one call path for every OpenAI chat completion in this service —
// structured (JSON schema), free-form text, streaming or not. See
// specs/AGENT_CHAT.md §3 for the target shape this collapses six hand-rolled call sites
// into. Call always takes ctx and always records cost itself, so the class of bug where a
// generate branch passed context.TODO() and silently recorded zero cost (BACKLOG.md 0.1)
// cannot be written again.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/billing"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/ili16/recipe-generator/internal/telemetry"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Client returns a new OpenAI SDK client. Cheap to construct; matches the pattern every
// call site used before (one client per call, no pooling needed for an HTTP client).
func Client() openai.Client {
	return openai.NewClient(option.WithAPIKey(os.Getenv("OPENAI_KEY")))
}

// Schema requests structured output: the completion's content is JSON conforming to it,
// decoded straight into Spec's T. Leave nil on a Spec for free-form text output (T must
// be string in that case — see Call's decode step).
type Schema struct {
	Name   string
	Schema map[string]any
}

// Nullable wraps a JSON schema type as "typ or null" — OpenAI's structured-output strict
// mode has no separate "optional" concept, so every Go *T field becomes anyOf[typ, null]
// in the schema instead. Was hand-duplicated once per schema-building file; one copy now.
func Nullable(typ string) map[string]any {
	return map[string]any{
		"anyOf": []any{map[string]any{"type": typ}, map[string]any{"type": "null"}},
	}
}

// Usage is the token accounting for one call, independent of pricing.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
}

// Spec describes one LLM call. It is deliberately not generic: the response shape is a
// type parameter on Call, so CallMessage can share every line of this with the agent
// loop, which wants the raw message (tool calls included) rather than a decoded value.
type Spec struct {
	Model    openai.ChatModel
	CallType string // telemetry + cost dimension, e.g. "generate", "refine"
	Messages []openai.ChatCompletionMessageParamUnion
	Schema   *Schema                          // nil ⇒ free-form text
	Tools    []openai.ChatCompletionToolParam // nil ⇒ no tool-calling
	OnDelta  func(string)                     // nil ⇒ non-streaming
}

// Call runs one chat completion per spec and decodes the assistant's text into T
// (JSON-unmarshal if Schema is set, verbatim if T is string). Everything else —
// streaming, telemetry, cost — is CallMessage's.
func Call[T any](ctx context.Context, spec Spec) (T, Usage, error) {
	var zero T
	msg, usage, err := CallMessage(ctx, spec)
	if err != nil {
		return zero, usage, err
	}
	result, err := decode[T](msg.Content)
	if err != nil {
		return zero, Usage{}, fmt.Errorf("parse %s response: %w", spec.CallType, err)
	}
	return result, usage, nil
}

// CallMessage runs one chat completion per spec: streams prose deltas through OnDelta if
// set, and returns the assistant message verbatim — content plus any tool calls the model
// asked for, which is what the agent loop (specs/AGENT_CHAT.md §2) iterates on. Records
// span/telemetry/cost unconditionally, against the authenticated caller found in ctx via
// auth.FromContext (a no-op for anonymous callers).
func CallMessage(ctx context.Context, spec Spec) (openai.ChatCompletionMessage, Usage, error) {
	client := Client()
	streaming := spec.OnDelta != nil

	slog.DebugContext(ctx, "llm: call start", "model", spec.Model, "call_type", spec.CallType, "messages", len(spec.Messages), "streaming", streaming)
	start := time.Now()

	ctx, span := telemetry.StartSpan(ctx, "llm.chat_completion")
	span.SetAttributes(attribute.String("llm.model", string(spec.Model)), attribute.String("llm.call_type", spec.CallType))
	defer span.End()

	fail := func(err error) (openai.ChatCompletionMessage, Usage, error) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, spec.CallType, string(spec.Model), time.Since(start).Milliseconds(), false)
		return openai.ChatCompletionMessage{}, Usage{}, err
	}

	params := openai.ChatCompletionNewParams{
		Messages: spec.Messages,
		Model:    spec.Model,
		Tools:    spec.Tools,
	}
	if spec.Tools != nil {
		// Reasoning models reject function tools on /v1/chat/completions unless
		// reasoning is explicitly disabled — "Function tools with reasoning_effort
		// are not supported ... set reasoning_effort to 'none'".
		params.ReasoningEffort = openai.ReasoningEffort("none")
	}
	if spec.Schema != nil {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openai.ResponseFormatJSONSchemaParam{
				JSONSchema: openai.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   spec.Schema.Name,
					Schema: spec.Schema.Schema,
					Strict: param.NewOpt(true),
				},
			},
		}
	}

	var msg openai.ChatCompletionMessage
	var usage Usage
	if streaming {
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: param.NewOpt(true)}
		stream := client.Chat.Completions.NewStreaming(ctx, params)
		defer stream.Close()

		// The SDK accumulator reassembles content and tool-call argument fragments,
		// which arrive split across chunks and interleaved by index.
		var acc openai.ChatCompletionAccumulator
		for stream.Next() {
			chunk := stream.Current()
			acc.AddChunk(chunk)
			if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
				spec.OnDelta(chunk.Choices[0].Delta.Content)
			}
		}
		if err := stream.Err(); err != nil {
			return fail(fmt.Errorf("%s stream: %w", spec.CallType, err))
		}
		if len(acc.Choices) == 0 {
			return fail(errors.New("no choices returned"))
		}
		usage = Usage{PromptTokens: acc.Usage.PromptTokens, CompletionTokens: acc.Usage.CompletionTokens}
		msg = acc.Choices[0].Message
	} else {
		completion, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			return fail(fmt.Errorf("%s completion: %w", spec.CallType, err))
		}
		if len(completion.Choices) == 0 {
			return fail(errors.New("no choices returned"))
		}
		usage = Usage{PromptTokens: completion.Usage.PromptTokens, CompletionTokens: completion.Usage.CompletionTokens}
		msg = completion.Choices[0].Message
	}

	telemetry.RecordLLMUsage(ctx, spec.CallType, string(spec.Model), usage.PromptTokens, usage.CompletionTokens)
	RecordCost(ctx, spec.CallType, string(spec.Model), usage.PromptTokens, usage.CompletionTokens)

	slog.DebugContext(ctx, "llm: call done", "call_type", spec.CallType, "dur_ms", time.Since(start).Milliseconds(),
		"tokens", usage.PromptTokens+usage.CompletionTokens, "tool_calls", len(msg.ToolCalls), "output", msg.Content)
	telemetry.RecordLLMCall(ctx, spec.CallType, string(spec.Model), time.Since(start).Milliseconds(), true)
	return msg, usage, nil
}

// decode unmarshals raw as JSON into T — except when T is string, where the raw text is
// the result verbatim (a free-form/no-schema call has nothing to parse).
func decode[T any](raw string) (T, error) {
	var v T
	if s, ok := any(&v).(*string); ok {
		*s = raw
		return v, nil
	}
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return v, err
	}
	return v, nil
}

// RecordCost estimates and persists the USD cost of one LLM call for the authenticated
// caller found in ctx. No-ops for anonymous requests — there's no user to attach cost to.
func RecordCost(ctx context.Context, callType, model string, promptTokens, completionTokens int64) {
	RecordCostUSD(ctx, callType, model, billing.CostUSD(model, promptTokens, completionTokens), promptTokens, completionTokens)
}

// RecordCostUSD is RecordCost with a pre-computed cost, for calls billing.CostUSD can't
// price from tokens alone (e.g. Whisper transcription, priced per call not per token).
func RecordCostUSD(ctx context.Context, callType, model string, costUSD float64, promptTokens, completionTokens int64) {
	u, ok := auth.FromContext(ctx)
	if !ok {
		return
	}
	if err := storage.RecordUsage(ctx, u.ID, callType, model, promptTokens, completionTokens, costUSD); err != nil {
		slog.Warn("record usage failed", "error", err)
	}
}
