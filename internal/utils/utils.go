package utils

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

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

// GetWebsiteContent fetches a URL and, when the page is a client-side SPA that
// loads its data via JS fetch() calls, follows those data URLs automatically.
func GetWebsiteContent(rawURL string) (string, error) {
	html, err := GetWebsite(rawURL)
	if err != nil {
		return "", err
	}
	if content, ok := resolveJSFetchContent(rawURL, html); ok {
		return content, nil
	}
	return html, nil
}

var (
	// Matches: const/let/var varName = urlParams.get('paramName')
	paramAssignRe = regexp.MustCompile(`(?:const|let|var)\s+(\w+)\s*=\s*urlParams\.get\(['"]([\w-]+)['"]\)`)
	// Matches: fetch(`...`) or fetch('...') – captures the URL string
	fetchBacktickRe = regexp.MustCompile("fetch\\(`([^`]+)`\\)")
	fetchStringRe   = regexp.MustCompile(`fetch\(['"]([^'"]+)['"]\)`)
	// Matches ${varName} inside a template literal
	tmplVarRe = regexp.MustCompile(`\$\{(\w+)\}`)
)

// resolveJSFetchContent looks for JS patterns like:
//
//	const foo = urlParams.get('bar'); fetch(`/data/${foo}.json`)
//
// and fetches those derived URLs when the query params are present.
func resolveJSFetchContent(rawURL, html string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || len(parsed.Query()) == 0 {
		return "", false
	}
	query := parsed.Query()
	origin := parsed.Scheme + "://" + parsed.Host

	// Build map: JS variable name → resolved value from query params
	varValues := map[string]string{}
	for _, m := range paramAssignRe.FindAllStringSubmatch(html, -1) {
		varName, paramName := m[1], m[2]
		if val := query.Get(paramName); val != "" {
			varValues[varName] = val
		}
	}
	// Also map query param names directly (some pages use params inline)
	for k, vs := range query {
		if len(vs) > 0 {
			varValues[k] = vs[0]
		}
	}
	if len(varValues) == 0 {
		return "", false
	}

	// Collect all fetch() URL templates from the page
	var templates []string
	for _, m := range fetchBacktickRe.FindAllStringSubmatch(html, -1) {
		templates = append(templates, m[1])
	}
	for _, m := range fetchStringRe.FindAllStringSubmatch(html, -1) {
		templates = append(templates, m[1])
	}

	for _, tmpl := range templates {
		resolved := tmplVarRe.ReplaceAllStringFunc(tmpl, func(placeholder string) string {
			varName := tmplVarRe.FindStringSubmatch(placeholder)[1]
			if v, ok := varValues[varName]; ok {
				return v
			}
			return placeholder
		})
		if strings.Contains(resolved, "${") {
			continue // unresolved placeholders remain
		}
		dataURL := resolved
		if strings.HasPrefix(resolved, "/") {
			dataURL = origin + resolved
		} else if !strings.HasPrefix(resolved, "http") {
			ref, _ := url.Parse(resolved)
			dataURL = parsed.ResolveReference(ref).String()
		}
		slog.Debug("generate/url: following JS data fetch", "data_url", dataURL)
		content, err := GetWebsite(dataURL)
		if err == nil && len(strings.TrimSpace(content)) > 50 {
			slog.Debug("generate/url: resolved data content", "bytes", len(content))
			return content, true
		}
	}
	return "", false
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
