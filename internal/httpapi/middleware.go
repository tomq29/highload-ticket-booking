package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// wrap is applied per route so that both the log line and the latency
// histogram can name the route without deriving it from the URL, which would
// put an unbounded number of paths into the metric labels.
//
// Successful requests log at debug: at load-test rates the access log is the
// first thing to become the bottleneck, and the interesting lines are the ones
// that failed.
func (s *Server) wrap(route string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}

		s.metrics.Started()
		defer func() {
			s.metrics.Done()

			if v := recover(); v != nil {
				s.log.Error("panic in handler",
					"route", route, "panic", v, "stack", string(debug.Stack()))
				if !rec.wrote {
					http.Error(rec, "internal error", http.StatusInternalServerError)
				}
			}

			took := time.Since(started)
			s.metrics.Observe(route, rec.status, took)
			s.log.Log(r.Context(), levelFor(rec.status), "request",
				"route", route,
				"status", rec.status,
				"duration_ms", took.Milliseconds())
		}()

		next(rec, r)
	})
}

func levelFor(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelDebug
	}
}

type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *recorder) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.status, r.wrote = status, true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}
