package utils

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/pemistahl/lingua-go"
)

func EncodeImageToBase64(imageData io.Reader) (string, error) {
	data, err := io.ReadAll(imageData)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	base64Data := base64.StdEncoding.EncodeToString(data)
	return base64Data, nil
}

func GetWebsite(link string) (string, error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return "", err
	}

	// Set headers to mimic a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")

	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	content, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func GetLanguage(prompt string) (string, error) {
	languages := []lingua.Language{
		lingua.English,
		lingua.French,
		lingua.German,
		lingua.Spanish,
	}

	detector := lingua.NewLanguageDetectorBuilder().
		FromLanguages(languages...).
		Build()

	language, exists := detector.DetectLanguageOf(prompt)

	if !exists {
		return "en", nil
	}

	return language.IsoCode639_1().String(), nil
}

func randomString() (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %v", err)
	}

	result := make([]byte, 8)
	for i := range b {
		result[i] = charset[int(b[i])%len(charset)]
	}

	return string(result), nil
}
