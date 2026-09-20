package utils

import (
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestSocialRouting(t *testing.T) {
	// handled=true means the generic HTML path is skipped — the thing that matters,
	// since that path "succeeds" on a JS shell and returns a bundle with no recipe.
	cases := []struct {
		url     string
		handled bool
	}{
		{"https://www.instagram.com/p/C1wWQcYrHXH/", true},
		{"https://instagram.com/reel/abc123/", true},
		{"https://www.tiktok.com/@cook/video/6718335390845095173", true},
		{"https://vm.tiktok.com/ZGJabc/", true},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", true},
		{"https://youtu.be/dQw4w9WgXcQ", true},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", true},
		{"https://www.youtube.com/@SomeChannel", false}, // no video id — generic path
		{"https://smittenkitchen.com/2024/01/soup/", false},
		{"https://notinstagram.com/p/x/", false}, // suffix match must not be substring
	}
	for _, c := range cases {
		_, _, handled := socialContent(c.url)
		if handled != c.handled {
			t.Errorf("%s: handled=%v, want %v", c.url, handled, c.handled)
		}
	}
}

func TestUnreadableSourceNamesTheWayOut(t *testing.T) {
	// A private or deleted post still ends here, and this error is the only thing the
	// user sees — it has to tell them what to do instead.
	if !strings.Contains(ErrUnreadableSource.Error(), "screenshot") {
		t.Error("ErrUnreadableSource must name the screenshot path")
	}
}

func TestMetaContent(t *testing.T) {
	page := `<meta property="og:title" content="Dave&#039;s Pizza Oven on Instagram: &quot;hi&quot;"/>` +
		`<meta content="39K likes" property="og:description">`
	if got := metaContent(page, "og:title"); got != `Dave's Pizza Oven on Instagram: "hi"` {
		t.Errorf("og:title unescaped wrong: %q", got)
	}
	if got := metaContent(page, "og:description"); got != "39K likes" {
		t.Errorf("reversed attribute order not matched: %q", got)
	}
	if got := metaContent(page, "og:image"); got != "" {
		t.Errorf("absent property should be empty, got %q", got)
	}
}

func TestYoutubeID(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":        "dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=42s":  "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ":                       "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=xyz":                "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":         "dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ":          "dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ":           "dQw4w9WgXcQ",
		"https://www.youtube.com/@SomeChannel":               "",
		"https://www.youtube.com/results?search_query=pasta": "",
	}
	for raw, want := range cases {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := youtubeID(u); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
}

// TestSocialLive hits the real oEmbed endpoints. Gated, as the DB and OpenAI tests are:
// it is the only check that fails when a platform changes its endpoint, which is the
// failure this package actually has. SOCIAL_LIVE=1 go test ./internal/utils/
func TestSocialLive(t *testing.T) {
	if os.Getenv("SOCIAL_LIVE") == "" {
		t.Skip("set SOCIAL_LIVE=1 to hit the real endpoints")
	}
	t.Run("tiktok caption", func(t *testing.T) {
		text, err, _ := socialContent("https://www.tiktok.com/@scout2015/video/6718335390845095173")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Scramble up ur name") {
			t.Errorf("caption missing from:\n%s", text)
		}
	})
	t.Run("instagram caption", func(t *testing.T) {
		// A real public reel. Instagram serves og: tags only to a crawler UA — a browser
		// UA gets a shell, which is what made this look impossible in 11.8.
		text, err, _ := socialContent("https://www.instagram.com/reel/DZfeR3zuoWw/")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "davespizzaoven") && !strings.Contains(text, "Dave") {
			t.Errorf("caption missing from:\n%s", text)
		}
	})
	t.Run("youtube title", func(t *testing.T) {
		text, err, _ := socialContent("https://www.youtube.com/watch?v=dQw4w9WgXcQ")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Never Gonna Give You Up") {
			t.Errorf("title missing from:\n%s", text)
		}
	})
}

// TestInstagramCaptionBlock is the embed page's shape, trimmed to the caption block: the
// author anchor, the caption with its <br /> paragraph breaks, and the comments link that
// ends it. Measured against a live reel on 2026-09-20 — the og: tags that used to carry
// this were simply absent on that post, which is why the embed page is now the first try.
func TestInstagramCaptionBlock(t *testing.T) {
	page := `<div class="Caption"><a class="CaptionUsername" href="/davespizzaoven/">davespizzaoven</a>` +
		`<br /><br />200g flour, 5g salt<br /><br />Bake 90 seconds &amp; eat` +
		`<div class="CaptionComments"><a href="#">View all 341 comments</a></div></div>`
	block := captionRe.FindStringSubmatch(page)
	if block == nil {
		t.Fatal("caption block not matched")
	}
	text := strings.ReplaceAll(block[1], "<br />", "\n")
	text = tagRe.ReplaceAllString(text, "\n")
	if !strings.Contains(text, "200g flour, 5g salt") || !strings.Contains(text, "Bake 90 seconds") {
		t.Errorf("caption lost its body: %q", text)
	}
	if strings.Contains(text, "View all 341 comments") {
		t.Errorf("comment link leaked into the caption: %q", text)
	}
}

func TestInstagramShortcode(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/reel/DZfeR3zuoWw/?stkn=abc":  "DZfeR3zuoWw",
		"https://www.instagram.com/p/C1wWQcYrHXH/":              "C1wWQcYrHXH",
		"https://www.instagram.com/davespizzaoven/reel/DZfe_x/": "DZfe_x",
		"https://www.instagram.com/davespizzaoven/":             "",
	}
	for url, want := range cases {
		got := ""
		if m := instagramShortcodeRe.FindStringSubmatch(url); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%s: shortcode %q, want %q", url, got, want)
		}
	}
}

// TestInstagramVideoURLUnescape is the embed page's own escaping, which arrives with the
// slashes under three backslashes and the query separator as &. Getting this wrong
// produced a URL that parsed but pointed nowhere, and a reel that read as having no video.
func TestInstagramVideoURLUnescape(t *testing.T) {
	page := `{"video_url\":\"https:\\\/\\\/scontent.cdninstagram.com\\\/o1\\\/v\\\/t2\\\/f2\\\/clip.mp4?_nc_cat=109&oh=00_AQK\",`
	v := videoURLRe.FindStringSubmatch(page)
	if v == nil {
		t.Fatal("video_url not matched")
	}
	got := strings.ReplaceAll(strings.ReplaceAll(v[1], `&`, "&"), `\`, "")
	want := "https://scontent.cdninstagram.com/o1/v/t2/f2/clip.mp4?_nc_cat=109&oh=00_AQK"
	if got != want {
		t.Errorf("unescaped to %q, want %q", got, want)
	}
	if u, err := url.Parse(got); err != nil || u.Host == "" {
		t.Errorf("unescaped URL does not parse: %v", err)
	}
}
