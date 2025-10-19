package tavily

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func jsonReader(v any) io.Reader {
	b := new(bytes.Buffer)

	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)

	enc.Encode(v)
	return b
}

func convertError(resp *http.Response) error {
	return errors.New(http.StatusText(resp.StatusCode))
}
