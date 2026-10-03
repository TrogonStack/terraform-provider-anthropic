package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	defaultBaseURL   = "https://api.anthropic.com"
	anthropicVersion = "2023-06-01"
)

type credential interface {
	authorize(req *http.Request)
}

type adminAPIKey string

func (k adminAPIKey) authorize(req *http.Request) {
	req.Header.Set("x-api-key", string(k))
}

type authToken string

func (t authToken) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+string(t))
}

var (
	errNoCredential        = errors.New("admin_api_key or auth_token must be set, either in the provider configuration or the ANTHROPIC_ADMIN_API_KEY and ANTHROPIC_AUTH_TOKEN environment variables")
	errConflictingCredential = errors.New("admin_api_key and auth_token are mutually exclusive; set only one of them or of ANTHROPIC_ADMIN_API_KEY and ANTHROPIC_AUTH_TOKEN")
)

func resolveCredential(apiKey, token string) (credential, error) {
	switch {
	case apiKey != "" && token != "":
		return nil, errConflictingCredential
	case apiKey != "":
		return adminAPIKey(apiKey), nil
	case token != "":
		return authToken(token), nil
	default:
		return nil, errNoCredential
	}
}

type apiClient struct {
	http       *http.Client
	baseURL    string
	credential credential
}

func newAPIClient(httpClient *http.Client, baseURL string, cred credential) *apiClient {
	return &apiClient{
		http:       httpClient,
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		credential: cred,
	}
}

func (c *apiClient) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")
	c.credential.authorize(req)
	return req, nil
}
