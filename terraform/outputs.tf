output "service_account" {
  value = google_service_account.main
}

output "service" {
  value = google_cloud_run_service.main
}

output "host" {
  value = regex("https://(.+-ew\\.a\\.run\\.app)", google_cloud_run_service.main.status[0].url)[0]
}

output "proj_hash" {
  value = regex("https://.+-([a-z0-9]{10})-ew\\.a\\.run\\.app", google_cloud_run_service.main.status[0].url)[0]
}
