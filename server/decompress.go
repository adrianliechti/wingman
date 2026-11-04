package server

import (
	"compress/gzip"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
)

func handleRequestDecompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoding := strings.ToLower(strings.TrimSpace(strings.Join(r.Header.Values("Content-Encoding"), ",")))
		switch encoding {
		case "", "identity":
			next.ServeHTTP(w, r)
			return
		case "gzip", "x-gzip", "zstd":
		default:
			writeDecompressionError(w, http.StatusUnsupportedMediaType, errors.New("unsupported Content-Encoding: use gzip, x-gzip, or zstd with one compression layer"))
			return
		}
		defer r.Body.Close()
		var decoded io.ReadCloser
		var err error
		switch encoding {
		case "gzip", "x-gzip":
			decoded, err = gzip.NewReader(r.Body)
		case "zstd":
			var reader *zstd.Decoder
			reader, err = zstd.NewReader(r.Body, zstd.WithDecoderMaxWindow(8<<20), zstd.WithDecoderConcurrency(1))
			if err == nil {
				decoded = reader.IOReadCloser()
			}
		}
		if err != nil {
			writeDecompressionError(w, http.StatusBadRequest, err)
			return
		}
		defer decoded.Close()
		r.Body = decoded
		r.ContentLength = -1
		r.Header.Del("Content-Encoding")
		r.Header.Del("Content-Length")
		next.ServeHTTP(w, r)
	})
}

func writeDecompressionError(w http.ResponseWriter, status int, err error) {

	data, encodeErr := json.Marshal(map[string]any{"error": map[string]any{"message": err.Error(), "type": "invalid_request_error", "code": status}})
	if encodeErr != nil {
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"message":"could not encode error response","type":"server_error","code":500}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(data)
}
