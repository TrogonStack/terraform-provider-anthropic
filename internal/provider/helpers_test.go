package provider

import "testing"

func TestWithoutPrefix(t *testing.T) {
	pattern := withoutPrefix("anthropic")
	cases := map[string]bool{
		"":               true,
		"team":           true,
		"a":              true,
		"anthro":         true,
		"anthropi":       true,
		"anthropix":      true,
		"Anthropic":      true,
		"my_anthropic":   true,
		"anthropic":      false,
		"anthropic_team": false,
	}
	for key, want := range cases {
		if got := pattern.MatchString(key); got != want {
			t.Errorf("withoutPrefix(%q).MatchString(%q) = %v, want %v", "anthropic", key, got, want)
		}
	}
}
