package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/go-retryablehttp"
)

func newRetryableClient() *http.Client {
	client := retryablehttp.NewClient()
	client.RetryMax = 5
	client.CheckRetry = retryPolicy
	client.Logger = nil
	return client.StandardClient()
}

func retryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err != nil {
		return true, nil
	}
	if resp.StatusCode == 429 {
		return true, nil
	}
	if resp.StatusCode >= 500 && resp.StatusCode != 501 {
		return true, nil
	}
	return false, nil
}
