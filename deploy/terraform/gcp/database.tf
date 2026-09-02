# The database, which is the first thing docs/deployment.md says a real
# deployment replaces.
#
# The dependencies component in deploy/k8s runs a PostgreSQL in the cluster so
# that a laptop needs nothing beside it. Here that component is not deployed —
# see deploy/k8s/overlays/cloud — and this is what stands in its place: a
# managed instance on a private address, reachable across the VPC peering the
# two resources below establish and from nowhere else.

resource "random_password" "database" {
  length = 32
  # The password is interpolated into a connection URL, so a character that has
  # to be percent-encoded there is a character that will one day be encoded once
  # too often or once too few.
  special = false
}

# Cloud SQL on a private address is a service in Google's own project, peered
# into this network. The range is reserved here and the peering is what makes it
# routable; without both, an instance with ipv4_enabled = false is an instance
# nothing can reach.
resource "google_compute_global_address" "database_peering" {
  name          = "${var.name}-database-peering"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.this.id
}

resource "google_service_networking_connection" "database" {
  network                 = google_compute_network.this.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.database_peering.name]

  depends_on = [google_project_service.this]
}

resource "google_sql_database_instance" "this" {
  name                = "${var.name}-${random_id.database.hex}"
  database_version    = "POSTGRES_17"
  region              = var.region
  deletion_protection = var.database_deletion_protection

  settings {
    tier              = var.database_tier
    disk_size         = var.database_disk_size
    disk_type         = "PD_SSD"
    disk_autoresize   = true
    availability_type = "ZONAL"

    ip_configuration {
      # No public address at all. The cluster's pods reach it across the
      # peering, and there is nothing else that should be reaching it.
      ipv4_enabled    = false
      private_network = google_compute_network.this.id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      start_time                     = "04:00"
    }
  }

  depends_on = [google_service_networking_connection.database]
}

# An instance name cannot be reused for a week after it is deleted, which turns
# every tear-down-and-try-again into a wait. The suffix is what makes a second
# apply possible on the same day.
resource "random_id" "database" {
  byte_length = 4
}

resource "google_sql_database" "quire" {
  name     = "quire"
  instance = google_sql_database_instance.this.name
}

resource "google_sql_user" "quire" {
  name     = "quire"
  instance = google_sql_database_instance.this.name
  password = random_password.database.result
}

locals {
  # sslmode=require and not verify-full, and that is a decision with a reason
  # rather than the easy answer. verify-full needs the instance's server
  # certificate in the container, and the node's image is distroless with a
  # read-only root and no volume for one; what require buys against a network
  # entirely inside this VPC is the encryption without the authentication. It is
  # the weaker half and it is written down here rather than discovered later.
  database_url = format(
    "postgres://%s:%s@%s:5432/%s?sslmode=require",
    google_sql_user.quire.name,
    random_password.database.result,
    google_sql_database_instance.this.private_ip_address,
    google_sql_database.quire.name,
  )
}
