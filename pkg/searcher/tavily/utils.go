package tavily

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

func convertError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	detail := strings.TrimSpace(string(body))
	if detail == "" {
		detail = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("tavily search: HTTP %d: %s", resp.StatusCode, detail)
}
