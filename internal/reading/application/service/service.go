// Package service declares the infrastructure ports the reading use cases
// depend on.
//
// One interface per port, and each of them is here rather than beside its
// implementation so that the dependency points inwards: a use case names what
// it needs, and internal/reading/infra/service supplies something that
// satisfies it.
//
// There are two, and the absence of a third is worth stating. Every other
// slice declares a Transaction, because it has a call that is one change to a
// reader and two rows in the database. This one has none: a mark is one row, a
// position is one row, and neither is written together with anything else — so
// a unit of work here would be a lock nothing needs and an interface nothing
// implements.
//
// Nothing here mentions a database. Repositories are ports too, but they
// belong to the entity they read, and are declared in
// internal/reading/domain/<entity>.
package service
