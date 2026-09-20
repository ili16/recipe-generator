package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/ili16/recipe-generator/internal/llm"
	"github.com/ili16/recipe-generator/internal/model"
	"github.com/openai/openai-go"
)

// maxScannedItems bounds one photo's answer. A shelf holds a few dozen things and a long
// receipt maybe sixty; anything past that is a misread, not a fuller inventory, and it
// would arrive as a wall of rows for the user to approve.
const maxScannedItems = 60

// ScannedItem is one line read off a shelf or a receipt: the same fields a pantry row
// carries, before any of them have been normalised or written.
type ScannedItem struct {
	Name      string   `json:"name"`
	Quantity  *float64 `json:"quantity"`
	Unit      *string  `json:"unit"`
	Category  string   `json:"category"`
	ExpiresOn *string  `json:"expires_on"`
}

// ScanPantryImage reads a photo of a shelf or a shop receipt into pantry lines
// (BACKLOG.md 17.5). It writes nothing — the caller shows the list to the user, and
// add_pantry_items is what puts any of it in the fridge.
//
// recipeModel, not assistModel: this is OCR over a crumpled receipt in someone's own
// language, and a misread name is not a cosmetic error — it lands in the pantry, fails
// to match the recipe that wanted it, and shops for it twice.
func ScanPantryImage(ctx context.Context, data []byte) ([]ScannedItem, error) {
	out, _, err := llm.Call[struct {
		Items []ScannedItem `json:"items"`
	}](ctx, llm.Spec{
		Model:    recipeModel,
		CallType: "pantry_scan",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(llm.PantryScanPrompt),
			openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.TextContentPart("List the foods in this photo."),
				openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: imageDataURL(data)}),
			}),
		},
		Schema: &llm.Schema{Name: "pantry_scan", Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"name":       map[string]any{"type": "string"},
							"quantity":   llm.Nullable("number"),
							"unit":       llm.Nullable("string"),
							"category":   map[string]any{"type": "string", "enum": model.PantryCategories},
							"expires_on": llm.Nullable("string"),
						},
						"required":             []string{"name", "quantity", "unit", "category", "expires_on"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"items"},
			"additionalProperties": false,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("scan pantry photo: %w", err)
	}

	items := make([]ScannedItem, 0, len(out.Items))
	for _, it := range out.Items {
		// A blank name is not a row anyone can approve, and NormalizeItem would drop it
		// at the write anyway.
		if model.NormalizeItem(it.Name) == "" {
			continue
		}
		if len(items) == maxScannedItems {
			break
		}
		items = append(items, it)
	}
	return items, nil
}

// imageDataURL renders raw image bytes as the data: URL the vision API takes. The mime
// is sniffed rather than trusted: the bytes reached us over /chat, and octet-stream is
// what DetectContentType says when it cannot tell — jpeg is the safe guess there.
func imageDataURL(data []byte) string {
	mime := http.DetectContentType(data)
	if mime == "application/octet-stream" {
		mime = "image/jpeg"
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}
