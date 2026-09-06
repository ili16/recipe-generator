package service

import "testing"

func TestResolveLanguage(t *testing.T) {
	cases := []struct {
		name, prompt, preferred, want string
	}{
		{"short foreign dish name defers to browser locale", "sauce bearnaise", "en", "en"},
		{"long clear-language sentence overrides browser locale", "ein rezept für sauce bearnaise", "en", "de"},
		{"empty prompt falls back to browser locale", "", "en", "en"},
		{"no browser locale, ambiguous prompt falls back to legacy self-detect", "sauce bearnaise", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveLanguage(c.prompt, c.preferred); got != c.want {
				t.Errorf("resolveLanguage(%q, %q) = %q, want %q", c.prompt, c.preferred, got, c.want)
			}
		})
	}
}
