terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "4.21.1"
    }
  }
}

provider "azurerm" {
  features {
  }
  subscription_id = var.subscription_id
}

data "azurerm_resource_group" "rg-recipe-generator" {
  name = "recipe-generator"
}

resource "azurerm_virtual_network" "recipegenerator" {
  name                = "recipegenerator-vnet"
  location            = "North Europe"
  resource_group_name = data.azurerm_resource_group.rg-recipe-generator.name
  address_space       = ["10.100.0.0/20"]
}

resource "azurerm_subnet" "vm" {
  name                 = "vm-subnet"
  resource_group_name  = data.azurerm_resource_group.rg-recipe-generator.name
  virtual_network_name = azurerm_virtual_network.recipegenerator.name
  address_prefixes     = ["10.100.4.0/24"]
}

resource "azurerm_network_interface" "jumphost" {
  name                = "jumphost-nic"
  location            = "North Europe"
  resource_group_name = data.azurerm_resource_group.rg-recipe-generator.name

  ip_configuration {
    name                          = "testconfiguration1"
    subnet_id                     = azurerm_subnet.vm.id
    private_ip_address_allocation = "Dynamic"
  }
}

resource "azurerm_virtual_machine" "main" {
  name                  = "jumphost-vm"
  location              = "North Europe"
  resource_group_name   = data.azurerm_resource_group.rg-recipe-generator.name
  network_interface_ids = [azurerm_network_interface.jumphost.id]
  vm_size               = "Standard_DS1_v2"

  # Uncomment this line to delete the OS disk automatically when deleting the VM
  delete_os_disk_on_termination = true

  # Uncomment this line to delete the data disks automatically when deleting the VM
  delete_data_disks_on_termination = true

  storage_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts"
    version   = "latest"
  }
  storage_os_disk {
    name              = "myosdisk1"
    caching           = "ReadWrite"
    create_option     = "FromImage"
    managed_disk_type = "Standard_LRS"
  }
  os_profile {
    computer_name  = "jumphost"
    admin_username = "ili16"
    admin_password = var.jumphost_password
  }
  os_profile_linux_config {
    disable_password_authentication = false
  }
}

resource "azurerm_subnet" "containerapp" {
  name                 = "containerapp-subnet"
  resource_group_name  = data.azurerm_resource_group.rg-recipe-generator.name
  virtual_network_name = azurerm_virtual_network.recipegenerator.name
  address_prefixes     = ["10.100.2.0/23"]
}

resource "azurerm_subnet" "pgsql-subnet" {
  name                 = "pgsql-subnet"
  resource_group_name  = data.azurerm_resource_group.rg-recipe-generator.name
  virtual_network_name = azurerm_virtual_network.recipegenerator.name
  address_prefixes     = ["10.100.0.0/24"]

  delegation {
    name = "delegation"

    service_delegation {
      name = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = [
        "Microsoft.Network/virtualNetworks/subnets/join/action",
      ]
    }
  }
  service_endpoints = ["Microsoft.Storage"]
}

resource "azurerm_private_dns_zone" "recipe-generator-dns" {
  name                = "recipe-generator-db.postgres.database.azure.com"
  resource_group_name = data.azurerm_resource_group.rg-recipe-generator.name
}

resource "azurerm_private_dns_zone_virtual_network_link" "recipe-generator-dns-link" {
  name                  = "recipe-generator-dns-link"
  resource_group_name   = data.azurerm_resource_group.rg-recipe-generator.name
  private_dns_zone_name = azurerm_private_dns_zone.recipe-generator-dns.name
  virtual_network_id    = azurerm_virtual_network.recipegenerator.id
}

resource "azurerm_postgresql_flexible_server" "psql-recipe-generator" {
  name                          = "pgsql-recipe-generator"
  resource_group_name           = data.azurerm_resource_group.rg-recipe-generator.name
  location                      = "North Europe"
  version                       = "16"
  delegated_subnet_id           = azurerm_subnet.pgsql-subnet.id
  private_dns_zone_id           = azurerm_private_dns_zone.recipe-generator-dns.id
  public_network_access_enabled = false
  administrator_login           = "psqladmin"
  administrator_password        = var.postgres_administrator_password
  zone                          = "1"

  storage_mb   = 32768
  storage_tier = "P4"

  sku_name = "B_Standard_B1ms"

  depends_on = [azurerm_private_dns_zone_virtual_network_link.recipe-generator-dns-link]
}

resource "azurerm_log_analytics_workspace" "recipe-generator-backend" {
  name                = "recipe-generator-analytics-switzerland"
  location            = "North Europe"
  resource_group_name = data.azurerm_resource_group.rg-recipe-generator.name
  sku                 = "PerGB2018"
  retention_in_days   = 30
}

resource "azurerm_container_app_environment" "recipe-generator" {
  name                       = "recipe-generator-app-env"
  location                   = "North Europe"
  resource_group_name        = data.azurerm_resource_group.rg-recipe-generator.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.recipe-generator-backend.id
  infrastructure_subnet_id   = azurerm_subnet.containerapp.id
}

resource "azurerm_container_app" "recipe-generator-backend" {
  name                         = "rg-backend-app"
  container_app_environment_id = azurerm_container_app_environment.recipe-generator.id
  resource_group_name          = data.azurerm_resource_group.rg-recipe-generator.name
  revision_mode                = "Single"

  template {
    container {
      name   = "recipe-generator-backendcontainer-app"
      image  = "ili16/recipe-generator:fd8ad3d050b4f73e6c9db6a58293746977c64346"
      cpu    = 0.25
      memory = "0.5Gi"
      env {
        name  = "OPENAI_KEY"
        value = var.openai_key
      }
      env {
        name  = "GITHUB_PAT"
        value = "stuff"
      }
      env {
        name = "DB_URL"
        value = "postgres://psqladmin:QNx7ZNNybdqZd7pTSpUz@pgsql-recipe-generator.postgres.database.azure.com:5432/postgres"
      }
    }
    http_scale_rule {
      concurrent_requests = "10"
      name                = "http-trigger"
    }
    min_replicas = 0
    max_replicas = 2
  }

  ingress {
    exposed_port     = 8080
    external_enabled = false
    target_port      = 8080
    transport        = "tcp"
    traffic_weight {
      percentage      = 100
      latest_revision = true
    }
  }

}

resource "azurerm_container_app" "recipe-generator-frontend" {
  name                         = "rg-frontend-app"
  container_app_environment_id = azurerm_container_app_environment.recipe-generator.id
  resource_group_name          = data.azurerm_resource_group.rg-recipe-generator.name
  revision_mode                = "Single"

  template {
    container {
      name   = "rg-frontendcontainer-app"
      image  = "ili16/recipe-generator-frontend-app:1.0.0"
      cpu    = 0.25
      memory = "0.5Gi"
    }
    http_scale_rule {
      concurrent_requests = "10"
      name                = "http-trigger"
    }
    min_replicas = 0
    max_replicas = 2
  }

  ingress {
    target_port      = 80
    external_enabled = true
    traffic_weight {
      percentage      = 100
      latest_revision = true
    }
  }
  lifecycle {
    ignore_changes = [secret]
  }
}

resource "azurerm_storage_account" "rg" {
  name                     = "recipegeneratorili16"
  resource_group_name      = data.azurerm_resource_group.rg-recipe-generator.name
  location                 = "North Europe"
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_container" "rg" {
  name                  = "static-websites"
  storage_account_id   = azurerm_storage_account.rg.id
  container_access_type = "private"
}
