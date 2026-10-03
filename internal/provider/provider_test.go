package provider

import (
	"errors"
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

func TestResolveCredential(t *testing.T) {
	cases := []struct {
		name    string
		apiKey  string
		token   string
		want    credential
		wantErr error
	}{
		{name: "admin API key", apiKey: "sk-ant-admin-test", want: adminAPIKey("sk-ant-admin-test")},
		{name: "auth token", token: "token-test", want: authToken("token-test")},
		{name: "neither", wantErr: errNoCredential},
		{name: "both", apiKey: "sk-ant-admin-test", token: "token-test", wantErr: errConflictingCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveCredential(tc.apiKey, tc.token)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("credential = %#v, want %#v", got, tc.want)
			}
		})
	}
}
