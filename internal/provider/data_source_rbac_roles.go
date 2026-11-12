package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/respjson"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &rbacRolesDataSource{}

func newRBACRolesDataSource() datasource.DataSource { return &rbacRolesDataSource{} }

type rbacRolesDataSource struct {
	client *anthropic.Client
}

type rbacRolesDataSourceModel struct {
	Id        types.String    `tfsdk:"id"`
	RBACRoles []rbacRoleModel `tfsdk:"rbac_roles"`
}

type rbacRoleModel struct {
	Id          types.String              `tfsdk:"id"`
	DisplayName types.String              `tfsdk:"display_name"`
	CreatedAt   types.String              `tfsdk:"created_at"`
	UpdatedAt   types.String              `tfsdk:"updated_at"`
	Permissions []rbacRolePermissionModel `tfsdk:"permissions"`
}

type rbacRolePermissionModel struct {
	Action   types.String                    `tfsdk:"action"`
	Resource rbacRolePermissionResourceModel `tfsdk:"resource"`
}

type rbacRolePermissionResourceModel struct {
	Type           types.String `tfsdk:"type"`
	OrganizationId types.String `tfsdk:"organization_id"`
	ConnectorId    types.String `tfsdk:"connector_id"`
	ToolName       types.String `tfsdk:"tool_name"`
	Scope          types.String `tfsdk:"scope"`
}

func (d *rbacRolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_roles"
}

func (d *rbacRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Lists the custom roles of a Claude Enterprise organization, with the permissions each role grants, through the Admin API.

Custom roles are read-only through the API: they are defined in claude.ai
organization settings or provisioned by Anthropic, and attached to RBAC groups
there. Each organization has its own role catalog. Reference a role by ` + "`id`" + `,
since Anthropic may change the ` + "`display_name`" + ` of a role it provisions.

Reading this data source makes one request for the role list and one for each
role's permissions. It needs a key created in claude.ai > Organization
settings > API with the ` + "`read:members`" + ` scope.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source.",
			},
			"rbac_roles": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The organization's custom roles, in the order the API returns them.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the RBAC role.",
						},
						"display_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Name of the RBAC role. For a role provisioned by Anthropic, this can differ from the label claude.ai shows, and Anthropic may change it.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the role was created.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the role was last updated.",
						},
						"permissions": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Permissions the role grants. The API omits rows for features not enabled for the organization. An `organization` permission with the action `capability_access_all` or `capability_access_all_ga` is a blanket grant of every product feature, or every generally available one, listed as that single row.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"action": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Action the permission grants on the resource, for example `chat`, `use`, `always_allow` or `grant`. The vocabulary depends on `resource.type`.",
									},
									"resource": schema.SingleNestedAttribute{
										Computed:            true,
										MarkdownDescription: "What the permission applies to.",
										Attributes: map[string]schema.Attribute{
											"type": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "Kind of resource: `organization`, `connector_tool`, `connector_scope`, `connector` or `all_connectors`.",
											},
											"organization_id": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "UUID of the organization. Null unless `type` is `organization`.",
											},
											"connector_id": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "ID of the connector. Null unless `type` is `connector_tool`, `connector_scope` or `connector`.",
											},
											"tool_name": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "Published name of the connector tool, server-encoded when it contains characters outside `[a-zA-Z0-9_-]`. Null unless `type` is `connector_tool`.",
											},
											"scope": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "OAuth scope the role may receive for the connector, server-encoded when it contains characters outside `[a-zA-Z0-9_-]`. Null unless `type` is `connector_scope`.",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *rbacRolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *rbacRolesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data rbacRolesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roles := []rbacRoleModel{}
	pager := d.client.Beta.Organization.RBACRoles.ListAutoPaging(ctx, anthropic.BetaOrganizationRBACRoleListParams{
		Limit: anthropic.Int(1000),
	})
	for pager.Next() {
		role := pager.Current()
		roles = append(roles, rbacRoleModel{
			Id:          types.StringValue(role.ID),
			DisplayName: types.StringValue(role.DisplayName),
			CreatedAt:   timestampValue(role.CreatedAt),
			UpdatedAt:   timestampValue(role.UpdatedAt),
		})
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list RBAC roles: %s", err))
		return
	}

	for i := range roles {
		permissions, err := d.listPermissions(ctx, roles[i].Id.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list permissions of RBAC role %s: %s", roles[i].Id.ValueString(), err))
			return
		}
		roles[i].Permissions = permissions
	}

	data.Id = types.StringValue("rbac_roles")
	data.RBACRoles = roles
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *rbacRolesDataSource) listPermissions(ctx context.Context, rbacRoleId string) ([]rbacRolePermissionModel, error) {
	permissions := []rbacRolePermissionModel{}
	pager := d.client.Beta.Organization.RBACRoles.Permissions.ListAutoPaging(ctx, rbacRoleId, anthropic.BetaOrganizationRBACRolePermissionListParams{
		Limit: anthropic.Int(1000),
	})
	for pager.Next() {
		permission := pager.Current()
		resource := permission.Resource
		permissions = append(permissions, rbacRolePermissionModel{
			Action: types.StringValue(permission.Action),
			Resource: rbacRolePermissionResourceModel{
				Type:           types.StringValue(resource.Type),
				OrganizationId: rbacRolePermissionField(resource.JSON.OrganizationID, resource.OrganizationID),
				ConnectorId:    rbacRolePermissionField(resource.JSON.ConnectorID, resource.ConnectorID),
				ToolName:       rbacRolePermissionField(resource.JSON.ToolName, resource.ToolName),
				Scope:          rbacRolePermissionField(resource.JSON.Scope, resource.Scope),
			},
		})
	}
	return permissions, pager.Err()
}

func rbacRolePermissionField(field respjson.Field, value string) types.String {
	if !field.Valid() {
		return types.StringNull()
	}
	return types.StringValue(value)
}
