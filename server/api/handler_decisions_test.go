package api

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
)

const testPNG = "iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAIAAACQkWg2AAAAHElEQVR4nGP4TyJgoKcGBgbsaFQDfTUQB4aDBgDAen2fpitsVAAAAABJRU5ErkJggg=="

func TestSystemOneAssetsAndStructuredData(t *testing.T) {
	for _, body := range []string{
		`{"state":"photo","assets":{"photo":{"type":"image","format":"png","data_base64":"` + testPNG + `"}},"questions":{"q":{"type":"box"}}}`,
		`{"state":{},"assets":{"photo":{"type":"image","format":"jpeg","data_base64":"aGk="}},"questions":{"q":{"type":"noul","criteria":{"true":{"image":"photo"}}}}}`,
		`{"state":{"type":"image","source":{"type":"base64","data":"ordinary JSON data"}},"questions":{"q":{"type":"score","criteria":["only"]}}}`,
	} {
		req, err := readDecisionsRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		input, err := req.input()
		if err != nil {
			t.Fatal(err)
		}
		if input.State == nil {
			t.Fatal("lost state")
		}
	}
	for _, body := range []string{
		`{"state":"photo","assets":{"photo":{"type":"audio","format":"png","data_base64":"aGk="}},"questions":{"q":{"type":"noul"}}}`,
		`{"state":"photo","assets":{"photo":{"type":"image","format":"webp","data_base64":"aGk="}},"questions":{"q":{"type":"noul"}}}`,
		`{"state":"photo","assets":{"photo":{"type":"image","format":"png","data_base64":"?"}},"questions":{"q":{"type":"noul"}}}`,
		`{"state":"photo","assets":{"photo":{"type":"image","format":"png","data_base64":""}},"questions":{"q":{"type":"noul"}}}`,
		`{"state":"photo","assets":{"photo":{"type":"image","part":"missing"}},"questions":{"q":{"type":"noul"}}}`,
		`{"state":"photo","questions":{"q":{"type":"box"}}}`,
	} {
		req, err := readDecisionsRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := req.input(); err == nil {
			t.Fatalf("accepted invalid assets: %s", body)
		}
	}
}

func TestSystemOneMultipart(t *testing.T) {
	png, _ := base64.StdEncoding.DecodeString(testPNG)
	for _, tc := range []struct {
		name, request string
		parts         []string
		valid         bool
	}{
		{"valid", `{"state":"photo","assets":{"photo":{"type":"image","part":"picture"}},"questions":{"q":{"type":"box"}}}`, []string{"picture"}, true},
		{"inline also allowed", `{"state":"photo","assets":{"photo":{"type":"image","format":"png","data_base64":"` + testPNG + `"}},"questions":{"q":{"type":"noul"}}}`, nil, true},
		{"part overrides base64", `{"state":"photo","assets":{"photo":{"type":"image","part":"picture","data_base64":"bad"}},"questions":{"q":{"type":"box"}}}`, []string{"picture"}, true},
		{"missing part", `{"state":"photo","assets":{"photo":{"type":"image","part":"picture"}},"questions":{"q":{"type":"box"}}}`, nil, false},
		{"unused part", `{"state":"x","questions":{"q":{"type":"noul"}}}`, []string{"picture"}, false},
		{"duplicate name", `{"state":"photo","assets":{"photo":{"type":"image","part":"picture"}},"questions":{"q":{"type":"box"}}}`, []string{"picture", "picture"}, false},
		{"reused part", `{"state":"photo","assets":{"photo":{"type":"image","part":"picture"},"second":{"type":"image","part":"picture"}},"questions":{"q":{"type":"noul"}}}`, []string{"picture"}, false},
		{"unnamed part", `{"state":"x","questions":{"q":{"type":"noul"}}}`, []string{""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, _ := writer.CreateFormField("request")
			io.WriteString(part, tc.request)
			for _, name := range tc.parts {
				part, _ := writer.CreateFormFile(name, "photo.png")
				part.Write(png)
			}
			writer.Close()
			r := httptest.NewRequest(http.MethodPost, "/", &body)
			r.Header.Set("Content-Type", writer.FormDataContentType())
			req, err := readDecisionsRequest(r)
			if err == nil {
				_, err = req.input()
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestSystemOneBoxSerialization(t *testing.T) {
	var value DecisionAnswer
	if err := json.Unmarshal([]byte(`{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":null}`), &value); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"confidence":null`) || strings.Contains(string(raw), `"boxes"`) {
		t.Fatalf("wrong box wire format: %s", raw)
	}
}
