package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
)

// gzipResponseWriter — обёртка для gzip-сжатия ответа.
type gzipResponseWriter struct {
	http.ResponseWriter
	Writer io.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

// WithGzip — middleware для поддержки gzip запросов/ответов.
func WithGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Распаковка входящего тела
		if strings.Contains(r.Header.Get("Content-Encoding"), "gzip") {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "bad gzip", http.StatusBadRequest)
				return
			}
			defer zr.Close()
			r.Body = zr
		}

		// Сжатие ответа, если клиент поддерживает
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			defer zw.Close()
			grw := &gzipResponseWriter{ResponseWriter: w, Writer: zw}
			next.ServeHTTP(grw, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
