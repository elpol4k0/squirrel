package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

var (
	mu      sync.RWMutex
	entries []entry
)

type entry struct {
	name   string
	labels map[string]string
	value  float64
	help   string
	kind   string // gauge | counter
}

func Set(name string, value float64, labels map[string]string, help string) {
	mu.Lock()
	defer mu.Unlock()
	for i, e := range entries {
		if e.name == name && labelsEqual(e.labels, labels) {
			entries[i].value = value
			return
		}
	}
	entries = append(entries, entry{name: name, labels: labels, value: value, help: help, kind: "gauge"})
}

func Inc(name string, labels map[string]string, help string) {
	Add(name, 1, labels, help)
}

func Add(name string, delta float64, labels map[string]string, help string) {
	mu.Lock()
	defer mu.Unlock()
	for i, e := range entries {
		if e.name == name && labelsEqual(e.labels, labels) {
			entries[i].value += delta
			return
		}
	}
	entries = append(entries, entry{name: name, labels: labels, value: delta, help: help, kind: "counter"})
}

// Handler returns an http.Handler that serves Prometheus text format.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		seen := map[string]bool{}
		for _, e := range entries {
			if !seen[e.name] {
				fmt.Fprintf(w, "# HELP %s %s\n", e.name, e.help)
				fmt.Fprintf(w, "# TYPE %s %s\n", e.name, e.kind)
				seen[e.name] = true
			}
			fmt.Fprintf(w, "%s%s %g\n", e.name, formatLabels(e.labels), e.value)
		}
	})
}

// Serve starts a Prometheus metrics HTTP server on addr (e.g. ":9090").
func Serve(addr string) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return srv.ListenAndServe()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	s := "{"
	first := true
	for k, v := range labels {
		if !first {
			s += ","
		}
		s += fmt.Sprintf(`%s="%s"`, k, v)
		first = false
	}
	return s + "}"
}

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
