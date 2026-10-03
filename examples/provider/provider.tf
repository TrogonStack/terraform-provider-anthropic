terraform {
  required_providers {
    anthropic = {
      source = "trogonstack/anthropic"
    }
  }
}

provider "anthropic" {
  auth_token = var.anthropic_auth_token
}

variable "anthropic_auth_token" {
  type      = string
  sensitive = true
}
