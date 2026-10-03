package provider

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"anthropic": providerserver.NewProtocol6WithError(New("test")()),
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
