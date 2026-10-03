package provider

import (
	"errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
)

func isNotFound(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}
