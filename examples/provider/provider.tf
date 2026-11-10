terraform {
  required_providers {
    vibeoncall = {
      source = "vibraniumlabs/vibeoncall"
    }
  }
}

# Reads the API key from VIBEONCALL_API_KEY when api_key is not set.
provider "vibeoncall" {}
