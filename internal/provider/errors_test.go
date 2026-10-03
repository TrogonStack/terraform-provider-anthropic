package provider

import (
	"errors"
	"testing"
)

func TestIsNotFound(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	ctx := t.Context()

	_, err := testAPIClient.Organization.Workspaces.Get(ctx, "wrkspc_missing")
	if !isNotFound(err) {
		t.Fatalf("expected Get for a missing workspace to return a not-found error, got: %v", err)
	}

	_, err = testAPIClient.Organization.Workspaces.Archive(ctx, "wrkspc_missing")
	if !isNotFound(err) {
		t.Fatalf("expected Archive for a missing workspace to return a not-found error, got: %v", err)
	}

	if isNotFound(errors.New("connection refused")) {
		t.Fatal("expected a non-API error not to count as not found")
	}
	if isNotFound(nil) {
		t.Fatal("expected nil not to count as not found")
	}
}
