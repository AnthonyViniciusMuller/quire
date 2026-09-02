# Where the two images are pushed to, unless the operator named somewhere else.
#
# One repository holding both, because Artifact Registry names an image by a
# path inside a repository rather than by a repository of its own — which is the
# shape difference from ECR next door and changes nothing about the tag. Both
# images carry one, because the schema is versioned with the binary that expects
# it: a deployment of one commit applies the migrations of that commit.
#
# Nothing here builds or pushes anything. `make images` in the repository root
# does that, and the README beside this file has the four commands; a Terraform
# apply that shelled out to Docker would be an apply whose result depended on
# what was in a local build cache.

resource "google_artifact_registry_repository" "this" {
  repository_id = var.name
  location      = var.region
  format        = "DOCKER"
  description   = "The Quire node and its schema."

  depends_on = [google_project_service.this]
}
