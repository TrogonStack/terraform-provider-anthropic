package provider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &usersDataSource{}

func newUsersDataSource() datasource.DataSource { return &usersDataSource{} }

type usersDataSource struct {
	client *anthropic.Client
}

type usersDataSourceModel struct {
	Id    types.String `tfsdk:"id"`
	Email types.String `tfsdk:"email"`
	Roles types.Set    `tfsdk:"roles"`
	Users []userModel  `tfsdk:"users"`
}

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists members of the organization through the Admin API, most recently added first, with optional filters. Needs `read:members` on a Claude Enterprise key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by member email. The API matches case-insensitively and tolerates common variants of the same address, such as a `+tag` suffix.",
			},
			"roles": schema.SetAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				MarkdownDescription: "Filter to members holding any of these roles. A Claude Console organization accepts `user`, `developer`, `billing`, `admin` and `claude_code_user`, and a Claude Enterprise organization accepts `user`, `owner`, `primary_owner`, `membership_admin` and `managed`.",
			},
			"users": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching members.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the member.",
						},
						"email": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Email of the member.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Name of the member.",
						},
						"role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Organization role of the member.",
						},
						"added_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the member joined the organization.",
						},
					},
				},
			},
		},
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data usersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationUserListParams{Limit: anthropic.Int(1000)}
	if isConfigured(data.Email) {
		params.Email = anthropic.String(data.Email.ValueString())
	}
	var roles []string
	if isConfigured(data.Roles) {
		resp.Diagnostics.Append(data.Roles.ElementsAs(ctx, &roles, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		slices.Sort(roles)
		params.Roles = roles
	}

	users := []userModel{}
	pager := d.client.Organization.Users.ListAutoPaging(ctx, params)
	for pager.Next() {
		users = append(users, userFromAPI(pager.Current()))
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list users: %s", err))
		return
	}

	data.Id = types.StringValue(usersDataSourceId(data.Email.ValueString(), roles))
	data.Users = users
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func usersDataSourceId(email string, roles []string) string {
	id := "users"
	if email != "" {
		id += "/email=" + email
	}
	if len(roles) > 0 {
		id += "/roles=" + strings.Join(roles, ",")
	}
	return id
}
