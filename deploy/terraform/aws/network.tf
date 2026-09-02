# The VPC this deployment lives in, and nothing else lives in.
#
# It is written out rather than taken from a registry module, for the same
# reason the manifests next door are: what a module hides here is which subnet
# a load balancer may be placed in and which one a database may not, and those
# are the two facts this file exists to state.
#
# **There is no NAT gateway, and the nodes hold addresses of their own.** That is
# not the shape this file had first, and it is worth being exact about what the
# change did and did not do rather than leaving somebody to infer it from a
# missing resource.
#
#   What is unchanged. Nothing reaches a node from outside. The cluster's own
#   security group admits the load balancer, the control plane and the other
#   nodes, and nothing else — inbound is closed by a rule, as it always was. The
#   database is still in the private subnets, still behind a security group that
#   names the cluster's, and still has no route to the internet at all.
#
#   What is not. A node is now addressable. A firewall rule that was wrong used
#   to need a pivot before it could be exploited and now does not, and there is
#   no longer one place through which everything this deployment sends outward
#   can be watched.
#
# It bought $35 a month on a deployment that runs for four of them. Putting the
# NAT back is an aws_eip, an aws_nat_gateway in a public subnet, a 0.0.0.0/0
# route on the private table, and moving the node group back — and it should be
# put back before this becomes anything anybody depends on.

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = { Name = var.name }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id

  tags = { Name = var.name }
}

# The nodes and the load balancer both live here now.
resource "aws_subnet" "public" {
  for_each = { for index, zone in local.azs : zone => index }

  vpc_id            = aws_vpc.this.id
  availability_zone = each.key
  cidr_block        = cidrsubnet(var.vpc_cidr, 4, each.value)
  # What gives a node its address, and therefore its way out.
  map_public_ip_on_launch = true

  tags = {
    Name = "${var.name}-public-${each.key}"
    # What the in-tree service controller looks for when it places an
    # internet-facing load balancer. Without it the Service for the node's
    # gateway stays pending forever, and the event says only that no subnet was
    # found.
    "kubernetes.io/role/elb" = "1"
  }
}

# The database, and nothing else. These subnets have no route off the VPC at
# all — not through a NAT, because there is none, and not through the internet
# gateway, because no route sends them there.
resource "aws_subnet" "private" {
  for_each = { for index, zone in local.azs : zone => index }

  vpc_id            = aws_vpc.this.id
  availability_zone = each.key
  cidr_block        = cidrsubnet(var.vpc_cidr, 4, each.value + var.availability_zones)

  tags = {
    Name                              = "${var.name}-private-${each.key}"
    "kubernetes.io/role/internal-elb" = "1"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }

  tags = { Name = "${var.name}-public" }
}

# No route but the local one, which every table has and none declares. A
# database that cannot reach the internet is a database nothing can quietly send
# anywhere.
resource "aws_route_table" "private" {
  vpc_id = aws_vpc.this.id

  tags = { Name = "${var.name}-private" }
}

resource "aws_route_table_association" "public" {
  for_each = aws_subnet.public

  subnet_id      = each.value.id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "private" {
  for_each = aws_subnet.private

  subnet_id      = each.value.id
  route_table_id = aws_route_table.private.id
}
