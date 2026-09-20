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
	"sync"

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

// GetWebsite fetches a user-supplied URL. Every outbound fetch of a URL this service did
// not choose itself goes through here — /generate's url field, the agent's fetch_url
// tool, and the JS-derived data URLs resolveJSFetchContent follows — so the SSRF guard in
// fetchguard.go sits on this one function. Do not reintroduce a bare http.Client.
func GetWebsite(link string) (string, error) {
	return GetWebsiteAs(link, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36")
}

// GetWebsiteAs is GetWebsite with the User-Agent chosen by the caller. Instagram serves a
// bare JS shell to a browser UA and the og: tags to a crawler one, so social.go needs this;
// everything else wants the browser UA and calls GetWebsite.
func GetWebsiteAs(link, userAgent string) (string, error) {
	if err := checkFetchURL(link); err != nil {
		return "", err
	}

	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", userAgent)

	res, err := fetchClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	// A status check belongs here, not in any one caller: without it a 404, a 429 or a
	// redirect to a login page returns ("", nil) and the caller builds a recipe out of an
	// empty string. Instagram's rate limiting is what surfaced this, but every fetched
	// recipe URL was exposed to it.
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("%w: %s", ErrFetchStatus, res.Status)
	}

	content, err := io.ReadAll(io.LimitReader(res.Body, MaxFetchBytes))
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// GetWebsiteContent fetches a URL and, when the page is a client-side SPA that
// loads its data via JS fetch() calls, follows those data URLs automatically.
func GetWebsiteContent(rawURL string) (string, error) {
	// A social link is not a page — see social.go. Handled first because the generic
	// path below "succeeds" on one, returning a JS bundle with no recipe in it.
	if text, err, handled := socialContent(rawURL); handled {
		return text, err
	}

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

// languageDetector builds the lingua-go detector once and reuses it — construction
// loads the language models and is expensive enough that doing it per call (the
// previous behavior) was a real cost on every generate request.
var languageDetector = sync.OnceValue(func() lingua.LanguageDetector {
	return lingua.NewLanguageDetectorBuilder().
		FromLanguages(lingua.English, lingua.French, lingua.German, lingua.Spanish).
		Build()
})

// DetectLanguage returns the ISO 639-1 code lingua-go confidently detects for
// prompt, or ("", false) if it can't (unsupported language, or a signal too
// weak to distinguish between the supported ones).
func DetectLanguage(prompt string) (string, bool) {
	language, exists := languageDetector().DetectLanguageOf(prompt)
	if !exists {
		return "", false
	}
	return strings.ToLower(language.IsoCode639_1().String()), true
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
