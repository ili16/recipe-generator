package model

import "mime/multipart"

type GenerateRecipeInput struct {
	Prompt string
	URL    string
	Image  multipart.File
}

type Recipe struct {
	Recipename string `json:"recipename"`
	Recipe     string `json:"recipe"`
	ID         int    `json:"id"`
	Transcript string `json:"transcript,omitempty"`
	Category   string `json:"category,omitempty"`
}
