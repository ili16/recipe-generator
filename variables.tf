variable "subscription_id" {
  type        = string
  description = "The Azure subscription ID"
}

variable "postgres_administrator_password" {
  type        = string
  description = "The password for the PostgreSQL server administrator"
}

variable "openai_key" {
  type        = string
  description = "The OpenAI API key"
}

variable "jumphost_password" {
    type        = string
    description = "The password for the jumphost"
}