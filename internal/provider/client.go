package provider

import (
	"errors"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const maxRetries = 5

var errConflictingCredential = errors.New("api_key and auth_token are mutually exclusive; set only one of them")

type clientConfig struct {
	apiKey    string
	authToken string
	baseURL   string
}

// newClient lets the SDK's credential chain (environment variables, profiles,
// workload identity federation) apply unless the configuration sets a credential.
func newClient(cfg clientConfig) (*anthropic.Client, error) {
	opts := []option.RequestOption{option.WithMaxRetries(maxRetries)}
	switch {
	case cfg.apiKey != "" && cfg.authToken != "":
		return nil, errConflictingCredential
	case cfg.apiKey != "":
		opts = append(opts, option.WithAPIKey(cfg.apiKey))
	case cfg.authToken != "":
		opts = append(opts, option.WithAuthToken(cfg.authToken))
	}
	if cfg.baseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.baseURL))
	}
	client := anthropic.NewClient(opts...)
	return &client, nil
}
