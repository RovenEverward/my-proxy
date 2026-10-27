package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// apiCompression only wraps authenticated management JSON. Backups stream
// through unchanged, and subscriptions never pass through this middleware.
func apiCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !apiAcceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &apiJSONWriter{ResponseWriter: w}
		next.ServeHTTP(cw, r)
		cw.finish()
	})
}

func apiAcceptsGzip(header string) bool {
	wildcard := false
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		name, q := strings.ToLower(strings.TrimSpace(parts[0])), 1.0
		for _, option := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(option), "=")
			if ok && strings.EqualFold(key, "q") {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					q = 0
				} else {
					q = parsed
				}
			}
		}
		if name == "gzip" {
			return q > 0
		}
		if name == "*" {
			wildcard = q > 0
		}
	}
	return wildcard
}

type apiJSONWriter struct {
	http.ResponseWriter
	body bytes.Buffer
	code int
	json bool
}

func (w *apiJSONWriter) WriteHeader(code int) {
	if code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.code != 0 {
		return
	}
	w.code = code
	w.json = strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") && w.Header().Get("Content-Encoding") == "" && code != http.StatusNoContent && code != http.StatusNotModified
	if !w.json {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *apiJSONWriter) Write(body []byte) (int, error) {
	if w.code == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if !w.json {
		return w.ResponseWriter.Write(body)
	}
	return w.body.Write(body)
}

func (w *apiJSONWriter) finish() {
	if !w.json {
		return
	}
	// JSON is already encoded by the original handler. Encoding it again here
	// would change escaping, indentation or trailing newlines.
	body := w.body.Bytes()
	if len(body) >= 1024 {
		var compressed bytes.Buffer
		z, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		_, _ = z.Write(body)
		_ = z.Close()
		body = compressed.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Del("Content-Length")
	}
	vary := strings.ToLower(w.Header().Get("Vary"))
	if !strings.Contains(","+strings.ReplaceAll(vary, " ", "")+",", ",accept-encoding,") && vary != "*" {
		w.Header().Add("Vary", "Accept-Encoding")
	}
	w.ResponseWriter.WriteHeader(w.code)
	_, _ = w.ResponseWriter.Write(body)
}
