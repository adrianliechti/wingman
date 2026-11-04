package tavily

import (
	"errors"
	"net/http"
)

func convertError(resp *http.Response) error {
	return errors.New(http.StatusText(resp.StatusCode))
}
