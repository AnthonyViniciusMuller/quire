// Package httpx serves what gRPC cannot.
//
// Three things have to be plain HTTP. The orchestrator probes a path, not a
// method, and it has to be able to do so while the gRPC server is refusing
// calls. A Prometheus scraper reads an exposition format over HTTP. And RFC
// 8615 defines discovery as documents under a /.well-known path, which is what
// lets one node find another knowing nothing but its domain — the whole
// mechanism of the federation rests on a URL a stranger can fetch.
//
// The server is therefore a second listener beside the gRPC one, on its own
// port, and the two are started and stopped together.
package httpx
