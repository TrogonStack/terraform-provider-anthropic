package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"anthropic": providerserver.NewProtocol6WithError(New("test")()),
}

// testProviderConfig is a minimal provider config that passes schema validation.
// The testAPIClient override bypasses provider configuration, so this value is unused.
const testProviderConfig = `
provider "anthropic" {
  api_key = "sk-ant-admin-unused"
}
`

func setupTestServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// setupTestClient sets the package-level testAPIClient with an SDK client pointing
// at the test server. Must be called before running terraform-plugin-testing steps.
func setupTestClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	client, err := newClient(clientConfig{apiKey: testAPIKey, baseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	testAPIClient = client
	t.Cleanup(func() { testAPIClient = nil })
}

func TestFakeRejectsMissingAPIKey(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	client, err := newClient(clientConfig{apiKey: "sk-ant-admin-wrong", baseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Organization.Workspaces.Get(t.Context(), "wrkspc_missing")
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected a request with the wrong API key to fail with 401, got: %v", err)
	}
}

func TestProviderServes(t *testing.T) {
	server, err := testAccProtoV6ProviderFactories["anthropic"]()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range schema.Diagnostics {
		t.Errorf("schema diagnostic: %s: %s", d.Summary, d.Detail)
	}
}

func TestNewClientRejectsBothCredentials(t *testing.T) {
	_, err := newClient(clientConfig{apiKey: "sk-ant-admin-test", authToken: "token-test"})
	if !errors.Is(err, errConflictingCredential) {
		t.Fatalf("err = %v, want %v", err, errConflictingCredential)
	}
}

func TestNewClientSendsTheConfiguredCredential(t *testing.T) {
	cases := []struct {
		name   string
		cfg    clientConfig
		header string
		want   string
	}{
		{"api key", clientConfig{apiKey: "sk-ant-admin-test"}, "X-Api-Key", "sk-ant-admin-test"},
		{"auth token", clientConfig{authToken: "token-test"}, "Authorization", "Bearer token-test"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "")
			t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

			var got http.Header
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"org_test","name":"Test","type":"organization"}`))
			}))
			t.Cleanup(server.Close)

			tc.cfg.baseURL = server.URL
			client, err := newClient(tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Organization.Get(t.Context()); err != nil {
				t.Fatal(err)
			}

			if v := got.Get(tc.header); v != tc.want {
				t.Errorf("%s = %q, want %q", tc.header, v, tc.want)
			}
			if got.Get("Anthropic-Version") == "" {
				t.Error("anthropic-version header is missing")
			}
		})
	}
}
