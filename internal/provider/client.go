package provider

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	maxRetries            = 5
	responseHeaderTimeout = 10 * time.Minute
)

var errConflictingCredential = errors.New("api_key and auth_token are mutually exclusive; set only one of them")

type clientConfig struct {
	apiKey    string
	authToken string
	baseURL   string
}

// newClient lets the SDK's credential chain (environment variables, profiles,
// workload identity federation) apply unless the configuration sets a credential.
// A configured credential disables the chain entirely, so no second credential
// from the environment or a profile is sent alongside it.
func newClient(cfg clientConfig) (*anthropic.Client, error) {
	if cfg.apiKey != "" && cfg.authToken != "" {
		return nil, errConflictingCredential
	}
	opts := []option.RequestOption{option.WithMaxRetries(maxRetries)}
	baseURL := cfg.baseURL
	if cfg.apiKey != "" || cfg.authToken != "" {
		opts = append(opts, option.WithoutEnvironmentDefaults(), option.WithHTTPClient(newHTTPClient()))
		if baseURL == "" {
			baseURL = os.Getenv("ANTHROPIC_BASE_URL")
		}
	}
	if cfg.apiKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.authToken != "" {
		opts = append(opts, option.WithAuthToken(cfg.authToken))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	client := anthropic.NewClient(opts...)
	return &client, nil
}

// newHTTPClient matches the client DefaultClientOptions installs, which
// WithoutEnvironmentDefaults skips.
func newHTTPClient() *http.Client {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		t = t.Clone()
		t.ResponseHeaderTimeout = responseHeaderTimeout
		return &http.Client{Transport: t}
	}
	return &http.Client{Transport: http.DefaultTransport}
}
