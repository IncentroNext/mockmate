locals {
  main_name = "mockmate-${var.service_version}"
}

/*
 * Service Accounts
 */
resource "google_service_account" "main" {
  project = var.project
  account_id = local.main_name
}

/*
 * Cloud Runs
 */
resource "google_cloud_run_service" "main" {
  project = var.project
  name = local.main_name
  location = var.region

  template {
    spec {
      containers {
        image = "gcr.io/${var.project}/${var.image}"
      }
      service_account_name = google_service_account.main.email
    }

    metadata {
      labels = {
        "app": local.main_name
        "release": var.release
      }
    }
  }

  traffic {
    percent = 100
    latest_revision = true
  }
}

/*
 * IAM Actions
 */


module "invokers" {
  source = "./invokers"

  for_each = zipmap(range(length(var.invokers)), var.invokers)
  service_account = each.value
  service = google_cloud_run_service.main.name
}

/*
 * Storage
 */

// Firestore
resource "google_project_iam_member" "main_firestore" {
  project = google_cloud_run_service.main.project
  role = "roles/datastore.user"
  member = "serviceAccount:${google_service_account.main.email}"
}
