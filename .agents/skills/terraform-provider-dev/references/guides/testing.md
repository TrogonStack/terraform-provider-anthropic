# Testing

## Overview

No resource exists in this provider yet, so none of the test infrastructure below has been written either. The plan, following the pattern sibling providers in this family use, is to test almost entirely against an in-memory fake of the Admin API, never against real Anthropic credentials in the normal test suite, with a small, separate set of live acceptance tests for pre-release sanity checks, gated behind `TF_ACC` and a real credential.

## Test Infrastructure

### The Fake Admin API

Sibling providers define a fake backend as an `http.Handler` that dispatches on request path, covering whatever endpoints the resources actually call. For this provider, that would mean a handler covering the `/v1/organizations/...` paths a first resource needs:

```go
type fakeAdminAPI struct {
    mu   sync.Mutex
    foos map[string]*fooRecord
}

func (f *fakeAdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    switch {
    case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/foos":
        f.createFoo(w, r)
    case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/organizations/foos/"):
        f.getFoo(w, r)
    // ... other endpoints as resources need them
    }
}
```

Whether this ends up as one handler covering every endpoint, or several smaller fakes, is undecided until there's a real resource to drive it. Error responses from the fake should match the real shape the official Go SDK parses: an HTTP status code with a body like `{"type":"error","error":{"type":"not_found_error","message":"..."}}`. Unlike a fake built around quirk-simulating boolean flags, prefer deriving error conditions from the actual state of the fake's in-memory data, the same way the real Admin API's errors come from the actual state of an organization.

### Provider Test Harness

This is the real, verified pattern from `provider_test.go`:

```go
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
    "anthropic": providerserver.NewProtocol6WithError(New("test")()),
}

const testProviderConfig = `
provider "anthropic" {
  api_key = "sk-ant-admin-test"
}
`
```

`testAPIClient` (declared in `provider.go` as `var testAPIClient *anthropic.Client`) is a package-level variable that `anthropicProvider.Configure` checks first, before falling back to `newClient` and the configured attributes (see `references/guides/provider-configuration.md`). Setting it is how acceptance tests run the full provider lifecycle (Create/Read/Update/Delete through real Terraform plans) against a fake server instead of the real Admin API, with no HTTP traffic leaving the test process. The rest of this guide uses two small test helpers, not yet written anywhere in the provider, that wrap the real pattern above for reuse across test functions:

```go
func setupTestServer(t *testing.T, handler http.Handler) *httptest.Server {
    t.Helper()
    server := httptest.NewServer(handler)
    t.Cleanup(server.Close)
    return server
}

func setupTestClient(t *testing.T, server *httptest.Server) {
    t.Helper()
    client, err := newClient(clientConfig{apiKey: "sk-ant-admin-test", baseURL: server.URL})
    if err != nil {
        t.Fatal(err)
    }
    testAPIClient = client
    t.Cleanup(func() { testAPIClient = nil })
}
```

`provider_test.go` also isolates credential-resolution tests from the real environment with `t.Setenv("ANTHROPIC_API_KEY", "")` and `t.Setenv("ANTHROPIC_AUTH_TOKEN", "")`, so a developer's own exported credentials never leak into a test run.

## Basic Test Structure

```go
func TestAccFoo_Basic(t *testing.T) {
    fake := &fakeAdminAPI{foos: map[string]*fooRecord{}}
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name = "test-foo"
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckResourceAttr("anthropic_foo.test", "name", "test-foo"),
                    resource.TestCheckResourceAttrSet("anthropic_foo.test", "id"),
                ),
            },
        },
    })
}
```

No `anthropic_foo` resource exists; this is the shape a first acceptance test is expected to take, one `TestStep` creating the resource and checking its attributes.

## Multi-Step Tests (Update Behavior)

```go
func TestAccFoo_Immutable(t *testing.T) {
    fake := &fakeAdminAPI{foos: map[string]*fooRecord{}}
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name           = "test-foo"
  immutable_flag = false
}
`,
            },
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name           = "test-foo"
  immutable_flag = true
}
`,
                ConfigPlanChecks: resource.ConfigPlanChecks{
                    PreApply: []plancheck.PlanCheck{
                        plancheck.ExpectResourceAction("anthropic_foo.test", plancheck.ResourceActionReplace),
                    },
                },
            },
        },
    })
}
```

This is the shape to reach for whenever a future resource has an attribute using `RequiresReplace` (see `references/guides/plan-modification.md`): step two changes that attribute, and `plancheck.ExpectResourceAction(..., plancheck.ResourceActionReplace)` asserts the plan modifier actually fires. No such attribute is known to exist on any resource yet.

## Import Tests

```go
{
    ResourceName:      "anthropic_foo.test",
    ImportState:       true,
    ImportStateVerify: true,
},
```

Append this step after a create step to verify `ImportStatePassthroughID` (or a compound import parser) round-trips correctly (`references/guides/state-management.md`). `ImportStateVerify: true` re-imports and diffs every attribute against the post-create state.

## Testing Drift (Changed or Removed Outside Terraform)

```go
func TestAccFoo_DeletedExternally(t *testing.T) {
    fake := &fakeAdminAPI{foos: map[string]*fooRecord{}}
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name = "test-foo"
}
`,
            },
            {
                PreConfig: func() {
                    fake.mu.Lock()
                    delete(fake.foos, "test-foo-id")
                    fake.mu.Unlock()
                },
                RefreshState:       true,
                ExpectNonEmptyPlan: true,
            },
        },
    })
}
```

Mutate the fake's backing map directly between steps (there is no real Admin API call to make in a test), then assert the next Read picks up the change and either reflects it into state or removes the resource entirely, driving a non-empty plan on the next `terraform plan`. Whether a given resource's "gone" case means `RemoveResource` or something softer depends on that resource's actual destroy semantics, which aren't decided yet (see `references/guides/resource-lifecycle.md`).

## Testing Unmanaged Attributes

```go
func TestAccFoo_UnmanagedFieldIsKept(t *testing.T) {
    fake := &fakeAdminAPI{foos: map[string]*fooRecord{}}
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name = "test-foo"
}
`,
            },
            {
                PreConfig: func() {
                    fake.mu.Lock()
                    fake.foos["test-foo-id"].SomeField = "set outside Terraform"
                    fake.mu.Unlock()
                },
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name = "test-foo"
}
`,
                ConfigPlanChecks: resource.ConfigPlanChecks{
                    PreApply: []plancheck.PlanCheck{
                        plancheck.ExpectEmptyPlan(),
                    },
                },
            },
        },
    })
}
```

This directly tests the "unmanaged attributes" pattern described in `references/guides/plan-modification.md`: an Optional+Computed field never set in configuration is never pushed by Update, and changing it directly on the fake backend produces an empty plan (not a diff Terraform tries to "fix"), because `UseStateForUnknown` carries the previous state forward and Read always adopts the live value.

## Testing Validators

```go
func TestAccFoo_EmptySetRejected(t *testing.T) {
    server := setupTestServer(t, &fakeAdminAPI{foos: map[string]*fooRecord{}})
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name       = "test-foo"
  member_ids = []
}
`,
                ExpectError: regexp.MustCompile(`(?i)at least 1`),
            },
        },
    })
}
```

A test like this is the way to assert a `setvalidator.SizeAtLeast(1)` failure (see `references/guides/validation.md`) without the fake ever seeing a request: validators run at plan time. No such validator exists in this provider yet.

## Unit Tests Without the Fake

Not every test needs an HTTP server. Plain table tests against constructed `error` values are the right shape for testing error-classification helpers directly, once they exist:

```go
func TestIsNotFound(t *testing.T) {
    tests := []struct {
        name string
        err  error
        want bool
    }{
        {"not found", &anthropic.Error{StatusCode: http.StatusNotFound}, true},
        {"conflict", &anthropic.Error{StatusCode: http.StatusConflict}, false},
        {"nil error", nil, false},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            if got := isNotFound(tt.err); got != tt.want {
                t.Errorf("isNotFound() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

`anthropic.Error` is real (an alias for the SDK's internal `apierror.Error`, with a `StatusCode int` field); `isNotFound` is a placeholder for whatever helper eventually wraps `errors.As(err, &apiErr)` plus a check on `StatusCode` and/or `apiErr.Type()`. The real not-found detection logic is undecided (see `references/guides/resource-lifecycle.md`).

## Retries

This provider carries no custom retry implementation and no dedicated test file for one. The official Go SDK retries its own requests; the provider only sets `option.WithMaxRetries(5)` in `newClient` (`client.go`). There is nothing provider-specific to unit test here: retry behavior on 429s and 5xxs is the SDK's responsibility, not this codebase's.

## Live Acceptance Tests

A small second suite, gated behind both `TF_ACC` and a real credential, is the expected shape for pre-release sanity checks against the real Admin API:

```go
func requireLiveCredentials(t *testing.T) string {
    t.Helper()
    if os.Getenv("TF_ACC") == "" {
        t.Skip("set TF_ACC=1 to run live acceptance tests")
    }
    key := os.Getenv("ANTHROPIC_API_KEY")
    if key == "" {
        t.Skip("set ANTHROPIC_API_KEY to run live acceptance tests")
    }
    return key
}

func TestLive_Foo(t *testing.T) {
    requireLiveCredentials(t)
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: liveProviderConfig + `
resource "anthropic_foo" "live" {
  name = "tf-provider-live-test"
}
`,
            },
        },
    })
}
```

No `live_test.go` exists yet. This suite is not expected to run in ordinary CI; it would require a human to export `TF_ACC=1` and a valid credential, either via `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` or one of the SDK's other credential-chain mechanisms, for a real, ideally disposable, organization.

## Check Functions Reference

| Function                            | Purpose                                      |
| -------------------------------------- | ------------------------------------------------ |
| `resource.TestCheckResourceAttr`     | Exact attribute value match                  |
| `resource.TestCheckResourceAttrSet`  | Attribute is non-empty                       |
| `resource.TestCheckNoResourceAttr`   | Attribute is absent/null                     |
| `resource.ComposeAggregateTestCheckFunc` | Combine checks, report all failures         |
| `plancheck.ExpectResourceAction`     | Assert plan action (replace, update, no-op)  |
| `plancheck.ExpectEmptyPlan`          | Assert no changes planned                    |
| Custom check funcs                  | Query the fake/live backend directly and assert on provider-specific state the schema doesn't expose as an attribute |

## Running Tests

```bash
go test ./internal/provider/...               # Fake-backed tests only
TF_ACC=1 go test ./internal/provider/... -run TestAcc  # Fake-backed acceptance tests, verbose plan/apply cycle
TF_ACC=1 ANTHROPIC_API_KEY=... go test ./internal/provider/... -run TestLive  # Live tests against the real Admin API
```

`TestAcc*` and `TestLive*` both use `resource.Test`, which requires `TF_ACC=1` to actually run (otherwise it skips with a message); `TestLive*` additionally requires a real credential via `requireLiveCredentials`. Plain unit tests (table tests, error-classification tests) run unconditionally with plain `go test`.

## Test Naming Convention

- `TestAcc<Resource>_<Scenario>`: acceptance tests against the fake (e.g. `TestAccFoo_Basic`, `TestAccFoo_Immutable`, `TestAccFoo_UnmanagedFieldIsKept`, `TestAccFoo_DeletedExternally`)
- `TestLive_<Resource>`: live acceptance tests against the real Admin API (e.g. `TestLive_Foo`)
- `Test<Thing>_<Condition>`: plain unit tests (e.g. `TestIsNotFound`, and the real `TestNewClientRejectsBothCredentials`/`TestNewClientSendsTheConfiguredCredential` in `provider_test.go`)

No resource exists yet, so none of the names above are real test functions; they illustrate the convention to follow.

## Related Framework References

| File                                             | Contents                        |
| ----------------------------------------------------- | ------------------------------------ |
| `framework/acctests/index.mdx`                   | Acceptance testing overview     |
| `framework/acctests/testing-patterns.mdx`        | Common testing patterns         |
| `framework/acctests/plan-checks.mdx`             | Plan check functions             |
