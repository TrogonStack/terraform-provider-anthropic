package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &workspaceMembersDataSource{}

func newWorkspaceMembersDataSource() datasource.DataSource { return &workspaceMembersDataSource{} }

type workspaceMembersDataSource struct {
	client *anthropic.Client
}

type workspaceMembersDataSourceModel struct {
	Id          types.String                `tfsdk:"id"`
	WorkspaceId types.String                `tfsdk:"workspace_id"`
	Members     []workspaceMemberEntryModel `tfsdk:"members"`
}

type workspaceMemberEntryModel struct {
	UserId        types.String `tfsdk:"user_id"`
	WorkspaceRole types.String `tfsdk:"workspace_role"`
}

func (d *workspaceMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_members"
}

func (d *workspaceMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the users who are members of an Anthropic workspace through the Admin API. Service account memberships are not included.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The workspace ID.",
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the workspace.",
			},
			"members": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The workspace's members, in the order the API returns them.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the user.",
						},
						"workspace_role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Role of the user in the workspace: `workspace_admin`, `workspace_billing`, `workspace_developer`, `workspace_restricted_developer`, or `workspace_user`.",
						},
					},
				},
			},
		},
	}
}

func (d *workspaceMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *workspaceMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data workspaceMembersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	members := []workspaceMemberEntryModel{}
	pager := d.client.Organization.Workspaces.Members.ListAutoPaging(ctx, data.WorkspaceId.ValueString(), anthropic.OrganizationWorkspaceMemberListParams{
		Limit: anthropic.Int(1000),
	})
	for pager.Next() {
		member := pager.Current()
		members = append(members, workspaceMemberEntryModel{
			UserId:        types.StringValue(member.UserID),
			WorkspaceRole: types.StringValue(string(member.WorkspaceRole)),
		})
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list workspace members: %s", err))
		return
	}

	data.Id = data.WorkspaceId
	data.Members = members
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
