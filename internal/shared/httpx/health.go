package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthonyvsmuller/quire/internal/shared/logging"
)

// probeTimeout bounds one round of readiness probes. It is deliberately
// shorter than the timeout an orchestrator gives the request, so that a
// dependency which has stopped answering is reported as not ready rather than
// as a probe that timed out — the two look the same from outside and mean
// different things.
const probeTimeout = 2 * time.Second

// Probe reports whether a dependency the node needs is answering. A nil error
// means it is.
type Probe func(ctx context.Context) error

// namedProbe is a probe together with the name it is reported under.
type namedProbe struct {
	name  string
	probe Probe
}

// livenessHandler answers whether the process is still able to answer.
//
// It consults nothing, and that is the design. Liveness failing means the
// orchestrator restarts the node, and a restart can only fix the process
// itself. If liveness checked the database, an outage of the database would
// restart every node of the federation in a loop — turning one dependency's
// failure into a failure of everything that depended on it, at the moment it
// could least afford it. Whether the dependencies are answering is
// [ReadinessPath]'s question, and its answer is to stop sending traffic, not
// to kill the process.
func livenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok\n")
	})
}

// readinessHandler answers whether traffic should be sent to this node now.
func readinessHandler(probes []namedProbe, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()

		ready := true

		var body strings.Builder

		for _, probe := range probes {
			err := probe.probe(ctx)
			if err != nil {
				ready = false

				// The reason stays in the log. A readiness endpoint is
				// reachable by whoever can reach the port, and the text of a
				// driver error names hosts, databases and users.
				logger.WarnContext(ctx, "a readiness probe failed",
					slog.String("probe", probe.name), logging.Err(err))
			}

			fmt.Fprintf(&body, "%s: %s\n", probe.name, outcome(err))
		}

		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}

		// A node with nothing to probe is ready, and says so rather than
		// answering with an empty body.
		if body.Len() == 0 {
			body.WriteString("ok\n")
		}

		writePlain(w, status, body.String())
	})
}

// outcome is how one probe is reported in the body.
func outcome(err error) string {
	if err != nil {
		return "unavailable"
	}

	return "ok"
}

// writePlain answers with a body an operator can read from a terminal, and
// tells every cache along the way that the answer is worth nothing a second
// later.
func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	_, _ = w.Write([]byte(body))
}
