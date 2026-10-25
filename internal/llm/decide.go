package llm

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ili16/recipe-generator/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// DecisionModel is the only model POST /v1/decisions accepts.
const DecisionModel = "gpt-6-luna"

// Choice is one option of a "choice" question.
type Choice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

// Question is one typed question against the shared input. Only "choice" is used today;
// "predicate" and "score" are the same request with a different Type and no Choices.
type Question struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Choices      []Choice `json:"choices,omitempty"`
}

// Answer is one question's typed answer, echoed back under its Name. A "refusal" answer
// carries neither Choice nor Confidence.
type Answer struct {
	Type       string  `json:"type"`
	Name       string  `json:"name"`
	Choice     string  `json:"choice"`
	Confidence float64 `json:"confidence"`
}

// Decide asks the Decisions API (POST /v1/decisions) every question about one input and
// returns the answers keyed by question name. It is a classifier, not a completion: the
// answer can only be one of the choices it was given, so there is no schema to enforce and
// no reworded name to guard against. The SDK predates the endpoint, hence the raw Post;
// cost and telemetry go through the same recorders CallMessage uses.
func Decide(ctx context.Context, callType, input string, questions []Question) (map[string]Answer, error) {
	start := time.Now()
	ctx, span := telemetry.StartSpan(ctx, "llm.decision")
	span.SetAttributes(attribute.String("llm.model", DecisionModel), attribute.String("llm.call_type", callType))
	defer span.End()

	var res struct {
		Answers []Answer `json:"answers"`
		Usage   struct {
			InputTokens int64 `json:"input_tokens"`
		} `json:"usage"`
	}
	client := Client()
	err := client.Post(ctx, "decisions", map[string]any{
		"model":     DecisionModel,
		"input":     input,
		"questions": questions,
	}, &res)
	if err != nil {
		err = fmt.Errorf("%s decision: %w", callType, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		telemetry.RecordLLMCall(ctx, callType, DecisionModel, time.Since(start).Milliseconds(), false)
		return nil, err
	}

	telemetry.RecordLLMUsage(ctx, callType, DecisionModel, res.Usage.InputTokens, 0)
	RecordCost(ctx, callType, DecisionModel, res.Usage.InputTokens, 0)
	telemetry.RecordLLMCall(ctx, callType, DecisionModel, time.Since(start).Milliseconds(), true)
	slog.DebugContext(ctx, "llm: decision done", "call_type", callType, "questions", len(questions),
		"answers", len(res.Answers), "dur_ms", time.Since(start).Milliseconds())

	byName := make(map[string]Answer, len(res.Answers))
	for _, a := range res.Answers {
		byName[a.Name] = a
	}
	return byName, nil
}
