package utils

import (
	"encoding/json"
	"errors"
	htmlpkg "html"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Social links are not pages. instagram.com, tiktok.com and youtube.com all serve a
// JavaScript shell to a plain GET, so the generic fetch path returns ~600KB of bundle
// with no caption, no description and (on Instagram) not even an og: tag — measured
// 2026-09-20, including with facebookexternalhit and Googlebot user agents.
//
// What each platform gives away for free, also measured:
//
//	TikTok    oembed endpoint, no key. `title` IS the caption. Enough to cook from.
//	YouTube   oembed endpoint, no key, but title/author only — no description, which
//	          is where a YouTube recipe actually lives. YOUTUBE_API_KEY (free quota,
//	          10K units/day) buys the description; without it we degrade to the title.
//	Instagram og:title and og:description — but ONLY to a crawler User-Agent. To a
//	          browser UA it serves the shell with no tags at all, which is why this was
//	          first measured as "nothing" (see 11.8). The title reads
//	          `Dave's Pizza Oven on Instagram: "<caption>"`, which is the whole post.
//
// ponytail: no cache table. A recipe URL is pasted once; the entry that proposed
// `social_media_cache` was pricing a re-fetch that does not happen. Add one when the
// YouTube quota is actually observed to run out.

// ErrUnreadableSource is a source this service can read nothing useful from, where
// saying so is better than handing the model a JS bundle to hallucinate over. Unlike
// ErrBlockedURL it carries a message meant to reach the user, because the user can
// act on it: screenshot the post and send the picture instead.
var ErrUnreadableSource = errors.New("that post could not be read — it may be private or deleted. Ask the user to screenshot it and send the image instead — reading a recipe out of a photo works")

var youtubeIDRe = regexp.MustCompile(`^[\w-]{11}$`)

// crawlerUA is what Instagram answers with og: tags. It is the same agent the platform
// expects from a link-preview fetcher, which is exactly what this is.
const crawlerUA = "facebookexternalhit/1.1"

// metaRe matches a <meta> tag's content for one og: property, in either attribute order.
func metaContent(page, prop string) string {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`<meta[^>]*property="` + prop + `"[^>]*content="([^"]*)"`),
		regexp.MustCompile(`<meta[^>]*content="([^"]*)"[^>]*property="` + prop + `"`),
	} {
		if m := re.FindStringSubmatch(page); m != nil {
			return strings.TrimSpace(htmlpkg.UnescapeString(m[1]))
		}
	}
	return ""
}

func instagramContent(rawURL string) (string, error) {
	page, err := GetWebsiteAs(rawURL, crawlerUA)
	if err != nil {
		// Instagram rate-limits per IP and answers 429 with a redirect to the login
		// page. Nothing to retry and nothing to degrade to — tell the user instead.
		return "", ErrUnreadableSource
	}
	// og:title carries `<author> on Instagram: "<caption>"`; og:description prefixes it with
	// like and comment counts. Take whichever is longer — on some posts only one is set.
	body := metaContent(page, "og:title")
	if d := metaContent(page, "og:description"); len(d) > len(body) {
		body = d
	}
	if body == "" {
		// A private, deleted or login-walled post. The user can still act on this.
		return "", ErrUnreadableSource
	}
	return join("Instagram post", "", body), nil
}

// socialContent returns readable text for a social URL, or ok=false for a URL that is
// not one (in which case the caller's generic HTML path handles it).
func socialContent(rawURL string) (string, error, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, false
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")

	switch {
	case host == "instagram.com" || strings.HasSuffix(host, ".instagram.com"):
		text, err := instagramContent(rawURL)
		return text, err, true

	case host == "tiktok.com" || strings.HasSuffix(host, ".tiktok.com"):
		text, err := tiktokContent(rawURL)
		return text, err, true

	case host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") || host == "youtu.be":
		id := youtubeID(u)
		if id == "" {
			return "", nil, false // a channel or search page — let the generic path try
		}
		text, err := youtubeContent(id, rawURL)
		return text, err, true
	}
	return "", nil, false
}

// oembed asks a platform's public oEmbed endpoint about one URL. Both endpoints used
// here take a key-less GET and answer with JSON. The fetch goes through GetWebsite so
// it inherits the one guarded client — see fetchguard.go.
func oembed(endpoint, target string) (map[string]any, error) {
	body, err := GetWebsite(endpoint + "?url=" + url.QueryEscape(target))
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

func tiktokContent(rawURL string) (string, error) {
	data, err := oembed("https://www.tiktok.com/oembed", rawURL)
	if err != nil {
		return "", err
	}
	caption := str(data, "title")
	if caption == "" {
		return "", errors.New("tiktok returned no caption")
	}
	return join("TikTok video", str(data, "author_name"), caption), nil
}

// youtubeID pulls the 11-character video id out of the shapes a user can paste:
// /watch?v=ID, youtu.be/ID, /shorts/ID, /embed/ID, /live/ID.
func youtubeID(u *url.URL) string {
	if v := u.Query().Get("v"); youtubeIDRe.MatchString(v) {
		return v
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if i == 0 && strings.EqualFold(u.Hostname(), "youtu.be") && youtubeIDRe.MatchString(p) {
			return p
		}
		if i > 0 && youtubeIDRe.MatchString(p) {
			switch strings.ToLower(parts[i-1]) {
			case "shorts", "embed", "live", "v":
				return p
			}
		}
	}
	return ""
}

func youtubeContent(id, rawURL string) (string, error) {
	// The description is the recipe; oEmbed does not carry it and the Data API does.
	if key := os.Getenv("YOUTUBE_API_KEY"); key != "" {
		if text, err := youtubeAPIContent(id, key); err == nil {
			return text, nil
		}
		// Quota exhausted, bad key, or a video the API will not describe — the
		// title from oEmbed is still better than the JS shell.
	}
	data, err := oembed("https://www.youtube.com/oembed", rawURL)
	if err != nil {
		return "", err
	}
	title := str(data, "title")
	if title == "" {
		return "", errors.New("youtube returned no title")
	}
	return join("YouTube video", str(data, "author_name"), title), nil
}

func youtubeAPIContent(id, key string) (string, error) {
	// The key is a query parameter, so this URL must never be logged or wrapped into
	// a returned error — GetWebsite does not log it, and every error here is discarded
	// by the caller rather than surfaced.
	body, err := GetWebsite("https://www.googleapis.com/youtube/v3/videos?part=snippet&id=" +
		url.QueryEscape(id) + "&key=" + url.QueryEscape(key))
	if err != nil {
		return "", errors.New("youtube api fetch failed")
	}
	var out struct {
		Items []struct {
			Snippet struct {
				Title        string `json:"title"`
				Description  string `json:"description"`
				ChannelTitle string `json:"channelTitle"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out.Items) == 0 {
		return "", errors.New("youtube api returned nothing")
	}
	s := out.Items[0].Snippet
	if s.Title == "" && s.Description == "" {
		return "", errors.New("youtube api returned an empty snippet")
	}
	return join("YouTube video", s.ChannelTitle, s.Title+"\n\n"+s.Description), nil
}

// join renders what a platform gave us as the few lines of text the model reads.
func join(kind, author, body string) string {
	var b strings.Builder
	b.WriteString(kind)
	if author != "" {
		b.WriteString(" by " + author)
	}
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(body))
	return b.String()
}
