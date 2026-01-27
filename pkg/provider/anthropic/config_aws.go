package anthropic

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/anthropics/anthropic-sdk-go/bedrock"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func bedrockMiddleware(token string) option.Middleware {
	return func(r *http.Request, next option.MiddlewareNext) (res *http.Response, err error) {
		var body []byte

		if r.Body != nil {
			body, err = io.ReadAll(r.Body)

			if err != nil {
				return nil, err
			}

			r.Body.Close()

			if !gjson.GetBytes(body, "anthropic_version").Exists() {
				body, _ = sjson.SetBytes(body, "anthropic_version", bedrock.DefaultVersion)
			}

			// pull the betas off of the header (if set) and put them in the body
			if betaHeader := r.Header.Values("anthropic-beta"); len(betaHeader) > 0 {
				r.Header.Del("anthropic-beta")
				body, err = sjson.SetBytes(body, "anthropic_beta", betaHeader)

				if err != nil {
					return nil, err
				}
			}

			if r.Method == http.MethodPost && bedrock.DefaultEndpoints[r.URL.Path] {
				model := gjson.GetBytes(body, "model").String()
				stream := gjson.GetBytes(body, "stream").Bool()

				body, _ = sjson.DeleteBytes(body, "model")
				body, _ = sjson.DeleteBytes(body, "stream")

				var method string

				if stream {
					method = "invoke-with-response-stream"
				} else {
					method = "invoke"
				}

				r.URL.Path = fmt.Sprintf("/model/%s/%s", model, method)
				r.URL.RawPath = fmt.Sprintf("/model/%s/%s", url.QueryEscape(model), method)
			}

			reader := bytes.NewReader(body)

			r.Body = io.NopCloser(reader)

			r.GetBody = func() (io.ReadCloser, error) {
				_, err := reader.Seek(0, 0)
				return io.NopCloser(reader), err
			}

			r.ContentLength = int64(len(body))
		}

		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}

		return next(r)
	}
}
