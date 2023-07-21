variable "tfc_organization" {

}

variable "tfc_workspace" {

}

locals {

  tfbuddy_yaml = yamlencode({
    workspaces = [
      {
        name         = var.tfc_workspace
        organization = var.tfc_organization
      }
    ]
  })

  terraform_tf = <<EOF
terraform {

  backend "remote" {
    organization = "${var.tfc_organization}"

    workspaces {
      name = "${var.tfc_workspace}"
    }
  }

}
EOF

  main_tf = <<EOF
resource "time_rotating" "moar_pets" {
  rotation_minutes = 1
}

resource "random_integer" "pet_length" {
  min = 2
  max = 5

  keepers = {
    # Generate a new integer for each time rotation
    rotate = time_rotating.moar_pets.id
  }
}

resource "random_pet" "rando" {
  count     = 4

  separator = "-"
  length    = random_integer.pet_length.result

  keepers = {
    # Generate new pets periodically
    rotate = time_rotating.moar_pets.id
  }
}

import {
  to = random_integer.import
  id = "15390,2,5"
}

resource "random_integer" "import" {
  min     = 2
  max     = 5
  keepers = {}
}

import {
  to = random_integer.import_replacement
  id = "15390,2,5"
}

resource "random_integer" "import_replacement" {
  min     = 1
  max     = 5
  keepers = {}
}

resource "tls_private_key" "rsa-4096-example" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "tls_self_signed_cert" "example" {
  private_key_pem = tls_private_key.rsa-4096-example.private_key_pem

  subject {
    common_name  = "example.com"
    organization = "ACME Examples, Inc"
  }

  validity_period_hours = 12

  early_renewal_hours = 5

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}

output "pets" {
  value = random_pet.rando.*.id
}
EOF
}

output "files" {
  value = {
    ".tfbuddy.yaml" = local.tfbuddy_yaml,
    "terraform.tf"  = local.terraform_tf,
    "main.tf"       = local.main_tf
  }
}
