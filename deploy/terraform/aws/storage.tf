# Where the e-books go, and the key pair that opens it.
#
# The node's S3 adapter carries no credential chain — its package comment says
# why, and the short version is that the SDK's chain lives in modules the node
# does not depend on. So this is the one place in the deployment where a
# long-lived credential exists, and it is scoped as narrowly as an IAM policy
# can express: three actions, on the objects of one bucket, for a user that can
# do nothing else in the account.

resource "aws_s3_bucket" "contents" {
  bucket = "${var.name}-contents-${data.aws_caller_identity.current.account_id}"
}

data "aws_caller_identity" "current" {}

resource "aws_s3_bucket_public_access_block" "contents" {
  bucket = aws_s3_bucket.contents.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "contents" {
  bucket = aws_s3_bucket.contents.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_versioning" "contents" {
  bucket = aws_s3_bucket.contents.id

  versioning_configuration {
    # Objects here are addressed by the digest of their content, so a write
    # never changes one and a version is only ever created by a delete. What it
    # buys is that a reader who deleted a work by mistake has not lost the file
    # yet.
    status = "Enabled"
  }
}

resource "aws_iam_user" "storage" {
  name = "${var.name}-storage"
}

resource "aws_iam_user_policy" "storage" {
  name = "quire-contents"
  user = aws_iam_user.storage.name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      # Exactly what service.BlobStore does: put one, open one, remove one. No
      # ListBucket — the node never enumerates the bucket, because what it holds
      # is addressed by a digest it already has.
      Action   = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"]
      Resource = "${aws_s3_bucket.contents.arn}/*"
    }]
  })
}

resource "aws_iam_access_key" "storage" {
  user = aws_iam_user.storage.name
}
