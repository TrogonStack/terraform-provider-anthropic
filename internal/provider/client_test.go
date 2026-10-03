package provider

import (
	"net/http"
	"testing"
)

func TestNewRequestAuthorizesWithTheCredential(t *testing.T) {
	cases := []struct {
		name       string
		credential credential
		header     string
		want       string
	}{
		{"admin API key", adminAPIKey("sk-ant-admin-test"), "x-api-key", "sk-ant-admin-test"},
		{"auth token", authToken("token-test"), "Authorization", "Bearer token-test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newAPIClient(http.DefaultClient, "https://api.example.test/", tc.credential)

			req, err := client.newRequest(t.Context(), http.MethodGet, "/v1/organizations/me", nil)
			if err != nil {
				t.Fatal(err)
			}

			if got := req.URL.String(); got != "https://api.example.test/v1/organizations/me" {
				t.Errorf("URL = %q", got)
			}
			if got := req.Header.Get(tc.header); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
			}
			if got := req.Header.Get("anthropic-version"); got != anthropicVersion {
				t.Errorf("anthropic-version = %q, want %q", got, anthropicVersion)
			}
		})
	}
}
