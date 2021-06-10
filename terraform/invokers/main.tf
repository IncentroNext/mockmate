resource "google_cloud_run_service_iam_member" "member" {
  project = var.service.project
  location = var.service.location
  service = var.service.name
  role = "roles/run.invoker"
  member = "serviceAccount:${var.service_account.email}"
}
