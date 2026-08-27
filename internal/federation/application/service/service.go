// Package service declares the infrastructure ports the federation use cases
// depend on.
//
// One interface per port, and each of them is here rather than beside its
// implementation so that the dependency points inwards: a use case names what
// it needs, and internal/federation/infra/service supplies something that
// satisfies it.
//
// Nothing here mentions a database. Repositories are ports too, but they
// belong to the entity they read, and are declared in
// internal/federation/domain/<entity>.
package service
