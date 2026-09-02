# Where the e-books go, and the identity that opens it.
#
# No key, and that is the one place this deployment is genuinely simpler than
# the one next door. The node's GCS adapter honours application default
# credentials, which on this cluster is a Workload Identity token — so the
# binding below is the whole of the credential and there is nothing to rotate,
# leak or write into a Secret. The S3 adapter carries no credential chain, for
# the reason its package comment gives, so the AWS stack has a key pair here
# instead.

resource "google_storage_bucket" "contents" {
  name          = "${var.name}-contents-${var.project}"
  location      = var.region
  force_destroy = false

  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  versioning {
    # Objects here are addressed by the digest of their content, so a write
    # never changes one and a version is only ever created by a delete. What it
    # buys is that a reader who deleted a work by mistake has not lost the file
    # yet.
    enabled = true
  }
}

resource "google_service_account" "node" {
  account_id   = "${var.name}-node"
  display_name = "The Quire node"
}

# Objects, and only in this bucket. objectAdmin rather than admin: the node
# writes, reads and removes objects, and never touches the bucket itself.
resource "google_storage_bucket_iam_member" "node" {
  bucket = google_storage_bucket.contents.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.node.email}"
}

# What ties the Kubernetes service account in deploy/k8s/base to the Google one
# above. The name on the left is fixed by the manifests — namespace `quire`,
# service account `quire` — and the annotation that completes the pair is
# patched onto it by the generated layer.
resource "google_service_account_iam_member" "node_workload_identity" {
  service_account_id = google_service_account.node.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project}.svc.id.goog[quire/quire]"
}
