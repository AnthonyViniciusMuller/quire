# The database, which is the first thing docs/deployment.md says a real
# deployment replaces.
#
# The dependencies component in deploy/k8s runs a PostgreSQL in the cluster so
# that a laptop needs nothing beside it. Here that component is not deployed —
# see deploy/k8s/overlays/cloud — and this is what stands in its place: a
# managed instance in the private subnets, reachable from the cluster's nodes
# and from nowhere else.

resource "random_password" "database" {
  length = 32
  # The password is interpolated into a connection URL, so a character that has
  # to be percent-encoded there is a character that will one day be encoded
  # once too often or once too few.
  special = false
}

resource "aws_db_subnet_group" "this" {
  name       = var.name
  subnet_ids = [for subnet in aws_subnet.private : subnet.id]
}

resource "aws_security_group" "database" {
  name        = "${var.name}-database"
  description = "PostgreSQL, reachable from the cluster's nodes"
  vpc_id      = aws_vpc.this.id

  tags = { Name = "${var.name}-database" }
}

# The cluster's own security group, which EKS creates and attaches to every
# node and every pod ENI. Naming it as the source rather than a CIDR is what
# keeps this rule true when the subnets change.
resource "aws_vpc_security_group_ingress_rule" "database" {
  security_group_id            = aws_security_group.database.id
  referenced_security_group_id = aws_eks_cluster.this.vpc_config[0].cluster_security_group_id
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
  description                  = "PostgreSQL from the cluster"
}

resource "aws_db_instance" "this" {
  identifier     = var.name
  engine         = "postgres"
  engine_version = "17"

  instance_class        = var.database_instance_class
  allocated_storage     = var.database_allocated_storage
  max_allocated_storage = var.database_allocated_storage * 5
  storage_type          = "gp3"
  storage_encrypted     = true

  db_name  = "quire"
  username = "quire"
  password = random_password.database.result

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.database.id]
  publicly_accessible    = false

  backup_retention_period = 7
  copy_tags_to_snapshot   = true
  auto_minor_version_upgrade = true

  deletion_protection = var.database_deletion_protection
  skip_final_snapshot = !var.database_deletion_protection

  # PostgreSQL 17 defaults to requiring TLS, and the connection string below
  # asks for it. Saying so twice is not redundant: sslmode=require without this
  # is a client that would have accepted a plaintext downgrade.
  parameter_group_name = aws_db_parameter_group.this.name

  apply_immediately = true
}

resource "aws_db_parameter_group" "this" {
  # A prefix rather than a name, because the lifecycle below creates the
  # replacement before destroying the original and two groups cannot share one
  # name.
  name_prefix = "${var.name}-postgres17-"
  family      = "postgres17"

  parameter {
    name  = "rds.force_ssl"
    value = "1"
    # Named rather than left to the default, because the default is "immediate"
    # and a static parameter refused immediately is an apply that fails on a
    # line that looks like it could not.
    apply_method = "pending-reboot"
  }

  lifecycle {
    create_before_destroy = true
  }
}

locals {
  # sslmode=require and not verify-full, and that is a decision with a reason
  # rather than the easy answer. verify-full needs the RDS certificate bundle in
  # the container, and the node's image is distroless with a read-only root and
  # no volume for one; what require buys against a network entirely inside this
  # VPC is the encryption without the authentication. It is the weaker half and
  # it is written down here rather than discovered later.
  database_url = format(
    "postgres://%s:%s@%s/%s?sslmode=require",
    aws_db_instance.this.username,
    random_password.database.result,
    aws_db_instance.this.endpoint,
    aws_db_instance.this.db_name,
  )
}
