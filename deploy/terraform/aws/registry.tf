# Where the two images are pushed to, unless the operator named somewhere else.
#
# Two repositories and one tag, because the schema is versioned with the binary
# that expects it: a deployment of one commit applies the migrations of that
# commit, and two tags that could drift would be two commits pretending to be
# one.
#
# Nothing here builds or pushes anything. `make images` in the repository root
# does that, and the README beside this file has the four commands; a Terraform
# apply that shelled out to Docker would be an apply whose result depended on
# what was in a local build cache.

resource "aws_ecr_repository" "quired" {
  name                 = "quired"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_repository" "migrate" {
  name                 = "quire-migrate"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}
