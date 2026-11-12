data "anthropic_users" "owners" {
  roles = ["owner", "primary_owner"]
}

output "owner_emails" {
  value = [for user in data.anthropic_users.owners.users : user.email]
}
