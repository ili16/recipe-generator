package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

// The Tools screen's HTTP surface and the agent's two kitchen tools. None of it touches the
// database or a model, so it is public and outside the LLM rate-limit stack.

// HandleListKitchenTools is GET /tools: the calculator catalog the screen renders forms from.
func HandleListKitchenTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"calculators": calcCatalog})
}

// HandleRunKitchenTool is POST /tools/{id} with {"inputs": {...}}.
func HandleRunKitchenTool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}
	outputs, inputs, err := runCalculator(r.PathValue("id"), body.Inputs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"id": r.PathValue("id"), "inputs": inputs, "outputs": outputs})
}

// HandleSeasonalTool is GET /tools/seasonal?month=1-12&category=vegetable|fruit|game&q=.
func HandleSeasonalTool(w http.ResponseWriter, r *http.Request) {
	m, ok := parseMonth(r.URL.Query().Get("month"))
	if !ok {
		http.Error(w, "month must be 1-12", http.StatusBadRequest)
		return
	}
	writeJSON(w, seasonal(m, r.URL.Query().Get("category"), r.URL.Query().Get("q")))
}

// parseMonth reads "1".."12"; empty means the current month in Germany.
func parseMonth(s string) (time.Month, bool) {
	if s == "" {
		return berlinNow().Month(), true
	}
	n, err := strconv.Atoi(s)
	return time.Month(n), err == nil && n >= 1 && n <= 12
}

func berlinNow() time.Time {
	if loc, err := time.LoadLocation("Europe/Berlin"); err == nil {
		return time.Now().In(loc)
	}
	return time.Now() // no tzdata in the image: UTC is at most a day off the month boundary
}

func toolCalculate(_ context.Context, _ *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Tool   string         `json:"tool"`
		Inputs map[string]any `json:"inputs"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	outputs, inputs, err := runCalculator(a.Tool, a.Inputs)
	if err != nil {
		return nil, nil, err // authored by runCalculator, safe to show
	}
	data := map[string]any{"tool": a.Tool, "inputs": inputs, "outputs": outputs}
	return data, &artifact{Kind: "tool", Data: data}, nil
}

func toolSeasonal(_ context.Context, _ *chatSession, args json.RawMessage) (any, *artifact, error) {
	var a struct {
		Month    int    `json:"month"`
		Category string `json:"category"`
		Query    string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, nil, errors.New("could not read the arguments")
	}
	m := berlinNow().Month()
	if a.Month != 0 {
		if a.Month < 1 || a.Month > 12 {
			return nil, nil, errors.New("month must be 1-12")
		}
		m = time.Month(a.Month)
	}
	return seasonal(m, a.Category, a.Query), nil, nil
}
