package server_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider/typesafe"
	"github.com/adrianliechti/wingman/server"
)

func TestSystemOneNativeImageRequests(t *testing.T) {
	const png = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAHElEQVR4nGP4TyJgoKcGBgbsaFQDfTUQB4aDBgDAen2fpitsVAAAAABJRU5ErkJggg=="
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			State  string `json:"state"`
			Assets map[string]struct {
				Type   string `json:"type"`
				Format string `json:"format"`
				Data   string `json:"data_base64"`
			} `json:"assets"`
			Questions map[string]struct {
				Type string `json:"type"`
			} `json:"questions"`
		}
		if err := json.UnmarshalRead(r.Body, &body); err != nil {
			t.Error(err)
		}
		if body.State != "photo" || body.Assets["photo"].Data != png || body.Questions["locate"].Type != "box" || r.Header.Get("TypeSafe-Danger-Zone-Images") != "true" {
			t.Errorf("lost native image request: %+v", body)
		}
		io.WriteString(w, `{"model":"jev-preview","answers":{"locate":{"type":"box","box":{"top":0.2,"bottom":0.66,"left":0.19,"right":0.68},"confidence":null}},"usage":{"input_tokens":285,"output_tokens":20}}`)
	}))
	defer upstream.Close()
	native, err := typesafe.NewDecider(upstream.URL, "jev-preview", typesafe.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterDecider("vision", native)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, multipartRequest := range []bool{false, true} {
		var request *http.Request
		if multipartRequest {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			p, _ := writer.CreateFormField("request")
			io.WriteString(p, `{"model":"vision","state":"photo","questions":{"locate":{"type":"box","instructions":"Find rectangle"}},"assets":{"photo":{"type":"image","part":"picture"}}}`)
			p, _ = writer.CreateFormFile("picture", "photo.png")
			image, _ := base64.StdEncoding.DecodeString(png)
			p.Write(image)
			writer.Close()
			request = httptest.NewRequest("POST", "/v1/systemone", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
		} else {
			request = httptest.NewRequest("POST", "/v1/systemone", strings.NewReader(`{"model":"vision","state":"photo","questions":{"locate":{"type":"box","instructions":"Find rectangle"}},"assets":{"photo":{"type":"image","format":"png","data_base64":"`+png+`"}}}`))
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, request)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"confidence":null`) || !strings.Contains(w.Body.String(), `"right":0.68`) || strings.Contains(w.Body.String(), `"boxes"`) {
			t.Fatalf("wrong box response: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("expected both native requests, got %d", calls)
	}
}
