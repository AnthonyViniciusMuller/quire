// Package service declares the infrastructure ports the identity use cases
// depend on.
//
// One interface per port, and each of them is here rather than beside its
// implementation so that the dependency points inwards: a use case names what
// it needs, and internal/identity/infra/service supplies something that
// satisfies it. Substituting bcrypt for another hashing algorithm, or the
// signing key for a test one, is then a change to a constructor in the
// container and to nothing else.
//
// Nothing here mentions a database. Repositories are ports too, but they belong
// to the entity they read, and are declared in
// internal/identity/domain/<entity>.
package service
