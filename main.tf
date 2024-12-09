terraform {
  required_providers {
    azurerm = {
      source = "hashicorp/azurerm"
      version = "4.13.0"
    }
  }
}

provider "azurerm" {
  features {}
  subscription_id = "52dde456-3457-4030-9aec-da35b594a559"
}

resource "azurerm_resource_group" "recipe-generator-backend" {
  name     = "recipe-generator-backend"
  location = "West Europe"
}

resource "azurerm_log_analytics_workspace" "recipe-generator-backend" {
  name                = "recipe-generator-backend-analytics"
  location            = azurerm_resource_group.recipe-generator-backend.location
  resource_group_name = azurerm_resource_group.recipe-generator-backend.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

resource "azurerm_container_app_environment" "recipe-generator-backend" {
  name                       = "recipe-generator-backend-env"
  location                   = azurerm_resource_group.recipe-generator-backend.location
  resource_group_name        = azurerm_resource_group.recipe-generator-backend.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.recipe-generator-backend.id
}

resource "azurerm_container_app" "recipe-generator-backend" {
  name                         = "recipe-generator-backend-app"
  container_app_environment_id = azurerm_container_app_environment.recipe-generator-backend.id
  resource_group_name          = azurerm_resource_group.recipe-generator-backend.name
  revision_mode                = "Single"

  template {
    container {
      name   = "recipe-generator-backendcontainer-app"
      image  = "ili16/recipe-generator:latest"
      cpu    = 0.25
      memory = "0.5Gi"
    }
  }
}