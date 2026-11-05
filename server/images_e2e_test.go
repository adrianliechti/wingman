package server_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/policy/noop"
	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/google"
	"github.com/adrianliechti/wingman/pkg/provider/openai"
	"github.com/adrianliechti/wingman/pkg/provider/openrouter"
	"github.com/adrianliechti/wingman/pkg/provider/xai"
	"github.com/adrianliechti/wingman/server"
)

type imageUpload struct {
	field string
	data  []byte
}

func imagePNG(t *testing.T, size int, annotation bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	if annotation {
		img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func imageRequest(t *testing.T, endpoint string, fields map[string]string, files []imageUpload) *http.Request {
	t.Helper()
	var body bytes.Buffer
	contentType := "application/json"
	if endpoint == "/v1/images/generations" {
		if err := json.MarshalWrite(&body, fields); err != nil {
			t.Fatal(err)
		}
	} else {
		form := multipart.NewWriter(&body)
		for name, value := range fields {
			if err := form.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range files {
			part, err := form.CreateFormFile(file.field, file.field+".png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(file.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		contentType = form.FormDataContentType()
	}
	req := httptest.NewRequest(http.MethodPost, endpoint, &body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "image/png")
	return req
}

type imageRedirectTransport struct{ target *url.URL }

func (rt imageRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme, clone.URL.Host = rt.target.Scheme, rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func imageServer(t *testing.T, backend string, handler http.HandlerFunc) http.Handler {
	t.Helper()
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: imageRedirectTransport{target}}
	var renderer provider.Renderer
	switch backend {
	case "openai":
		renderer, err = openai.NewRenderer(upstream.URL, "gpt-image-2.5-flare", openai.WithMaxRetries(0))
	case "gpt-image-2.6-flare", "gpt-image-2.6-sunburst":
		renderer, err = openai.NewRenderer(upstream.URL, backend, openai.WithMaxRetries(0))
	case "google":
		renderer, err = google.NewRenderer("gemini-nano-banana-2.1", google.WithToken("test"), google.WithClient(client))
	case "openrouter":
		renderer, err = openrouter.NewRenderer("test-image", openrouter.WithClient(client))
	case "xai":
		renderer, err = xai.NewRenderer("test-image", xai.WithClient(client))
	}
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Policy: noop.New()}
	cfg.RegisterRenderer("image-test", renderer)
	s, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type imageWireRequest struct {
	prompt string
	images [][]byte
	mask   []byte
	fields map[string]string
}

func readImageWire(t *testing.T, backend string, r *http.Request) imageWireRequest {
	t.Helper()
	got := imageWireRequest{fields: map[string]string{}}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Error(err)
			return got
		}
		defer r.MultipartForm.RemoveAll()
		for k, v := range r.MultipartForm.Value {
			got.fields[k] = v[0]
		}
		for field, headers := range r.MultipartForm.File {
			for _, header := range headers {
				f, err := header.Open()
				if err != nil {
					t.Error(err)
					continue
				}
				data, err := io.ReadAll(f)
				f.Close()
				if err != nil {
					t.Error(err)
				}
				if header.Header.Get("Content-Type") != "image/png" {
					t.Errorf("%s MIME = %q", field, header.Header.Get("Content-Type"))
				}
				switch field {
				case "image[]":
					got.images = append(got.images, data)
				case "mask":
					got.mask = data
				default:
					t.Errorf("unexpected upload field %q", field)
				}
			}
		}
		got.prompt = got.fields["prompt"]
		return got
	}
	var body map[string]jsontext.Value
	if err := json.UnmarshalRead(r.Body, &body); err != nil {
		t.Error(err)
		return got
	}
	for k, v := range body {
		var s string
		if json.Unmarshal(v, &s) == nil {
			got.fields[k] = s
		}
	}
	got.prompt = got.fields["prompt"]
	decodeURL := func(s string) []byte {
		prefix, encoded, ok := strings.Cut(s, ",")
		if !ok || prefix != "data:image/png;base64" {
			t.Errorf("invalid image data URL prefix %q", prefix)
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Error(err)
		}
		return data
	}
	switch backend {
	case "google":
		var contents []struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					Data     []byte `json:"data"`
					MIMEType string `json:"mimeType"`
				} `json:"inlineData"`
			} `json:"parts"`
		}
		if err := json.Unmarshal(body["contents"], &contents); err != nil || len(contents) != 1 {
			t.Errorf("invalid contents: %s (%v)", body["contents"], err)
			return got
		}
		for _, part := range contents[0].Parts {
			got.prompt += part.Text
			if part.InlineData != nil {
				if part.InlineData.MIMEType != "image/png" {
					t.Errorf("input MIME = %q", part.InlineData.MIMEType)
				}
				got.images = append(got.images, part.InlineData.Data)
			}
		}
		var cfg struct {
			ResponseModalities []string `json:"responseModalities"`
			ImageConfig        struct {
				AspectRatio string `json:"aspectRatio"`
				ImageSize   string `json:"imageSize"`
			} `json:"imageConfig"`
		}
		if err := json.Unmarshal(body["generationConfig"], &cfg); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(cfg.ResponseModalities, []string{"IMAGE"}) {
			t.Errorf("response modalities = %v", cfg.ResponseModalities)
		}
		got.fields["aspect_ratio"], got.fields["resolution"] = cfg.ImageConfig.AspectRatio, cfg.ImageConfig.ImageSize
	case "openrouter":
		var refs []struct {
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if raw := body["input_references"]; raw != nil {
			if err := json.Unmarshal(raw, &refs); err != nil {
				t.Error(err)
			}
		}
		for _, ref := range refs {
			got.images = append(got.images, decodeURL(ref.ImageURL.URL))
		}
	case "xai":
		if raw := body["image"]; raw != nil {
			var img struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &img); err != nil {
				t.Error(err)
			}
			got.images = append(got.images, decodeURL(img.URL))
		}
	}
	return got
}

func TestImagesEndToEnd(t *testing.T) {
	base, markup := imagePNG(t, 4, false), imagePNG(t, 4, true)
	reference := imagePNG(t, 3, true)
	for _, backend := range []string{"openai", "google", "openrouter", "xai"} {
		for _, endpoint := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/render"} {
			for _, guides := range []string{"none", "mask", "markup", "both"} {
				if endpoint == "/v1/images/generations" && guides != "none" {
					continue
				}
				t.Run(backend+endpoint+"/"+guides, func(t *testing.T) {
					calls := 0
					var wire imageWireRequest
					s := imageServer(t, backend, func(w http.ResponseWriter, r *http.Request) {
						calls++
						wire = readImageWire(t, backend, r)
						w.Header().Set("Content-Type", "application/json")
						encoded := base64.StdEncoding.EncodeToString(base)
						if backend == "google" {
							fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":%q}}]}}]}`, encoded)
						} else {
							fmt.Fprintf(w, `{"data":[{"b64_json":%q,"media_type":"image/png"}]}`, encoded)
						}
					})
					fields := map[string]string{"model": "image-test", "prompt": "add a sun", "size": "1024x1024", "aspect_ratio": "16:9", "quality": "xhigh", "resolution": "2K", "background": "transparent", "output_format": "png", "response_format": "url"}
					var files []imageUpload
					editing := endpoint == "/v1/images/edits" || guides != "none"
					if editing {
						field := "image[]"
						if endpoint == "/v1/render" {
							field = "file"
						}
						files = append(files, imageUpload{field, base})
						if backend != "xai" {
							files = append(files, imageUpload{field, reference})
						}
					}
					hasMask, hasMarkup := guides == "mask" || guides == "both", guides == "markup" || guides == "both"
					if hasMask {
						files = append(files, imageUpload{"mask", markup})
					}
					if hasMarkup {
						files = append(files, imageUpload{"markup", markup})
					}
					w := httptest.NewRecorder()
					s.ServeHTTP(w, imageRequest(t, endpoint, fields, files))
					if backend == "xai" && hasMask {
						if w.Code != 400 || calls != 0 || !strings.Contains(w.Body.String(), "mask is not supported") {
							t.Fatalf("response=%d %s calls=%d", w.Code, w.Body.String(), calls)
						}
						return
					}
					if w.Code != 200 || calls != 1 {
						t.Fatalf("response=%d %s calls=%d", w.Code, w.Body.String(), calls)
					}
					if endpoint == "/v1/render" {
						if !bytes.Equal(w.Body.Bytes(), base) || w.Header().Get("Content-Type") != "image/png" {
							t.Fatal("raw image response changed")
						}
					} else {
						var response struct {
							Data []struct {
								URL string `json:"url"`
							} `json:"data"`
						}
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data) != 1 || response.Data[0].URL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(base) {
							t.Fatalf("invalid response: %s", w.Body.String())
						}
					}
					if !strings.HasSuffix(wire.prompt, "add a sun") {
						t.Errorf("prompt=%q", wire.prompt)
					}
					if hasMarkup && !strings.Contains(wire.prompt, "annotations") {
						t.Errorf("missing markup instruction: %q", wire.prompt)
					}
					wantImages := 0
					if editing {
						wantImages = 1
						if backend != "xai" {
							wantImages++
						}
					}
					if hasMask && backend != "openai" {
						wantImages++
					}
					if hasMask && hasMarkup {
						wantImages++
					}
					if len(wire.images) != wantImages {
						t.Fatalf("images=%d want=%d", len(wire.images), wantImages)
					}
					if editing {
						if backend != "xai" && !bytes.Equal(wire.images[1], reference) {
							t.Error("reference order or content changed")
						}
						if hasMarkup && !hasMask {
							if bytes.Equal(wire.images[0], base) {
								t.Error("markup was not composited")
							}
						} else if !bytes.Equal(wire.images[0], base) {
							t.Error("original image changed")
						}
					}
					if hasMask && backend == "openai" && !bytes.Equal(wire.mask, markup) {
						t.Error("native mask changed or disappeared")
					}
					if hasMask && backend != "openai" {
						mask, err := png.Decode(bytes.NewReader(wire.images[len(wire.images)-1]))
						if err != nil {
							t.Fatal(err)
						}
						black, _, _, _ := mask.At(0, 0).RGBA()
						white, _, _, _ := mask.At(1, 1).RGBA()
						if black != 0 || white != 0xffff {
							t.Error("emulated mask polarity is wrong")
						}
					}
					wantFields := map[string]string{}
					switch backend {
					case "openai":
						wantFields = map[string]string{"model": "gpt-image-2.5-flare", "size": "2560x1440", "quality": "xhigh", "background": "transparent", "output_format": "png"}
					case "google":
						wantFields = map[string]string{"aspect_ratio": "16:9", "resolution": "2K"}
					case "openrouter":
						wantFields = map[string]string{"model": "test-image", "aspect_ratio": "16:9", "resolution": "2K", "quality": "xhigh", "background": "transparent", "output_format": "png"}
					case "xai":
						wantFields = map[string]string{"model": "test-image", "response_format": "b64_json"}
					}
					for field, want := range wantFields {
						if wire.fields[field] != want {
							t.Errorf("%s=%q want=%q", field, wire.fields[field], want)
						}
					}
				})
			}
		}
	}
}

func TestGPTImage26GenerationEndToEnd(t *testing.T) {
	var animation bytes.Buffer
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	if err := gif.EncodeAll(&animation, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{12, 12}}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, tt := range []struct {
			name, format, mime, size, aspect, quality, wantSize string
			content                                             []byte
			frames                                              bool
		}{
			{"gif", "gif", "image/gif", "256x256", "", "max", "512x512", animation.Bytes(), true},
			{"gif defaults", "gif", "image/gif", "auto", "", "", "384x384", animation.Bytes(), false},
			{"gif landscape", "gif", "image/gif", "", "16:9", "low", "256x144", animation.Bytes(), true},
			{"gif portrait", "gif", "image/gif", "1080x1920", "", "high", "288x512", animation.Bytes(), true},
			{"svg", "svg", "image/svg+xml", "1024x1024", "", "max", "1024x1024", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), false},
			{"png", "png", "image/png", "1024x1024", "", "max", "1024x1024", imagePNG(t, 4, false), false},
		} {
			for _, response := range []string{"b64_json", "url", "raw"} {
				t.Run(model+"/"+tt.name+"/"+response, func(t *testing.T) {
					calls := 0
					s := imageServer(t, model, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if !strings.HasSuffix(r.URL.Path, "/images/generations") {
							t.Errorf("upstream path = %s", r.URL.Path)
						}
						var body map[string]any
						if err := json.UnmarshalRead(r.Body, &body); err != nil {
							t.Error(err)
						}
						want := map[string]any{"model": model, "prompt": "draw a loop", "output_format": tt.format, "complexity": "low", "size": tt.wantSize}
						if tt.format == "svg" {
							want["svg"] = map[string]any{"model": "gpt-6.1-sol"}
						} else if body["svg"] != nil {
							t.Errorf("unexpected SVG options: %v", body["svg"])
						}
						if tt.quality != "" {
							want["quality"] = tt.quality
						} else if body["quality"] != nil {
							t.Errorf("unexpected quality: %v", body["quality"])
						}
						if tt.frames {
							want["frame_count"], want["frame_duration_ms"] = float64(2), float64(120)
						} else if body["frame_count"] != nil || body["frame_duration_ms"] != nil {
							t.Errorf("unexpected animation controls: %v", body)
						}
						for key, value := range want {
							if !reflect.DeepEqual(body[key], value) {
								t.Errorf("%s = %v, want %v", key, body[key], value)
							}
						}
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(tt.content))
					})
					fields := map[string]any{"model": "image-test", "prompt": "draw a loop", "output_format": tt.format, "complexity": "low", "response_format": response}
					if tt.format == "svg" {
						fields["svg"] = map[string]string{"model": "gpt-6.1-sol"}
					}
					for key, value := range map[string]string{"size": tt.size, "aspect_ratio": tt.aspect, "quality": tt.quality} {
						if value != "" {
							fields[key] = value
						}
					}
					if tt.frames {
						fields["frame_count"], fields["frame_duration_ms"] = 2, 120
					}
					body, err := json.Marshal(fields)
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(body))
					r.Header.Set("Content-Type", "application/json")
					if response == "raw" {
						form := url.Values{}
						for key, value := range fields {
							if key != "svg" {
								form.Set(key, fmt.Sprint(value))
							}
						}
						if tt.format == "svg" {
							form.Set("svg_model", "gpt-6.1-sol")
						}
						r = httptest.NewRequest(http.MethodPost, "/v1/render", strings.NewReader(form.Encode()))
						r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						r.Header.Set("Accept", tt.mime)
					}
					w := httptest.NewRecorder()
					s.ServeHTTP(w, r)
					if w.Code != 200 || calls != 1 {
						t.Fatalf("response = %d %s, calls = %d", w.Code, w.Body.String(), calls)
					}
					if response == "raw" {
						if !bytes.Equal(w.Body.Bytes(), tt.content) || w.Header().Get("Content-Type") != tt.mime {
							t.Fatal("raw output bytes or MIME type changed")
						}
						return
					}
					var result struct {
						Data []map[string]string `json:"data"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Data) != 1 {
						t.Fatalf("invalid response: %s", w.Body.String())
					}
					want := base64.StdEncoding.EncodeToString(tt.content)
					if response == "url" {
						want = "data:" + tt.mime + ";base64," + want
					}
					if result.Data[0][response] != want {
						t.Errorf("output bytes or MIME type changed: %s", w.Body.String())
					}
				})
			}
		}
	}
}

func TestGPTImage26EditEndToEnd(t *testing.T) {
	base := imagePNG(t, 4, false)
	for _, model := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, format := range []string{"png", "gif", "svg"} {
			t.Run(model+"/"+format, func(t *testing.T) {
				calls := 0
				s := imageServer(t, model, func(w http.ResponseWriter, r *http.Request) {
					calls++
					wire := readImageWire(t, "openai", r)
					for key, want := range map[string]string{"model": model, "quality": "xhigh", "complexity": "medium", "background": "transparent", "output_format": "png", "size": "2560x1440"} {
						if wire.fields[key] != want {
							t.Errorf("%s = %q, want %q", key, wire.fields[key], want)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(base))
				})
				w := httptest.NewRecorder()
				s.ServeHTTP(w, imageRequest(t, "/v1/images/edits", map[string]string{"model": "image-test", "prompt": "draw", "complexity": "medium", "quality": "xhigh", "aspect_ratio": "16:9", "resolution": "2K", "background": "transparent", "output_format": format}, []imageUpload{{"image", base}}))
				wantStatus, wantCalls := 200, 1
				if format != "png" {
					wantStatus, wantCalls = 400, 0
				}
				if w.Code != wantStatus || calls != wantCalls {
					t.Fatalf("response = %d %s, calls = %d", w.Code, w.Body.String(), calls)
				}
			})
		}
	}
}

func TestRenderRequestFormsEndToEnd(t *testing.T) {
	base := imagePNG(t, 4, false)
	for _, kind := range []string{"urlencoded", "query", "raw image", "malformed multipart"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			s := imageServer(t, "openai", func(w http.ResponseWriter, r *http.Request) {
				calls++
				wire := readImageWire(t, "openai", r)
				if wire.prompt != "draw" {
					t.Errorf("prompt=%q", wire.prompt)
				}
				if kind == "raw image" {
					if len(wire.images) != 1 || !bytes.Equal(wire.images[0], base) {
						t.Error("raw image missing")
					}
				} else if len(wire.images) != 0 {
					t.Error("generation unexpectedly includes images")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(base))
			})
			r := httptest.NewRequest(http.MethodPost, "/v1/render?model=image-test&input=draw", nil)
			switch kind {
			case "urlencoded":
				r = httptest.NewRequest(http.MethodPost, "/v1/render", strings.NewReader("model=image-test&input=draw"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			case "raw image":
				r = httptest.NewRequest(http.MethodPost, r.URL.String(), bytes.NewReader(base))
				r.Header.Set("Content-Type", "application/octet-stream")
			case "malformed multipart":
				r = httptest.NewRequest(http.MethodPost, r.URL.String(), strings.NewReader("--broken\r\nContent-Disposition: form-data; name=\"mask\"; filename=\"mask.png\"\r\n\r\nincomplete"))
				r.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if kind == "malformed multipart" {
				if w.Code != 400 || calls != 0 {
					t.Fatalf("response=%d calls=%d", w.Code, calls)
				}
			} else if w.Code != 200 || calls != 1 {
				t.Fatalf("response=%d calls=%d body=%s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestImagesEndToEndErrors(t *testing.T) {
	base := imagePNG(t, 4, false)
	for _, backend := range []string{"openai", "google", "openrouter", "xai"} {
		for _, endpoint := range []string{"/v1/images/generations", "/v1/images/edits", "/v1/render"} {
			for _, status := range []int{400, 429, 500, 503} {
				t.Run(fmt.Sprintf("%s%s/%d", backend, endpoint, status), func(t *testing.T) {
					calls := 0
					s := imageServer(t, backend, func(w http.ResponseWriter, r *http.Request) {
						calls++
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(status)
						fmt.Fprintf(w, `{"error":{"code":%d,"status":"INVALID_ARGUMENT","type":"invalid_request_error","message":"upstream image failure"}}`, status)
					})
					var files []imageUpload
					if endpoint == "/v1/images/edits" {
						files = []imageUpload{{"image", base}}
					}
					w := httptest.NewRecorder()
					s.ServeHTTP(w, imageRequest(t, endpoint, map[string]string{"model": "image-test", "prompt": "draw a sun"}, files))
					want := status
					if status == 500 {
						want = 502
					}
					if w.Code != want || calls == 0 || !strings.Contains(w.Body.String(), "upstream image failure") {
						t.Fatalf("status=%d want=%d calls=%d body=%s", w.Code, want, calls, w.Body.String())
					}
					if endpoint != "/v1/render" {
						var response struct {
							Error struct {
								Type    string `json:"type"`
								Message string `json:"message"`
							} `json:"error"`
						}
						if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
							t.Fatal(err)
						}
						wantType := map[int]string{400: "invalid_request_error", 429: "rate_limit_exceeded", 500: "server_error", 503: "service_unavailable_error"}[status]
						if response.Error.Type != wantType || w.Header().Get("Content-Type") != "application/json" {
							t.Errorf("error envelope = %s", w.Body.String())
						}
					}
				})
			}
		}
	}
}

func TestImageEditRejectsInvalidGuidesEndToEnd(t *testing.T) {
	base, small := imagePNG(t, 4, false), imagePNG(t, 2, false)
	for _, backend := range []string{"openai", "google", "openrouter"} {
		for _, endpoint := range []string{"/v1/images/edits", "/v1/render"} {
			for _, invalid := range []string{"missing image", "mask size", "markup size", "invalid mask", "invalid markup"} {
				t.Run(backend+endpoint+"/"+invalid, func(t *testing.T) {
					s := imageServer(t, backend, func(w http.ResponseWriter, r *http.Request) {
						t.Error("invalid request reached upstream")
						w.WriteHeader(500)
					})
					field := "image"
					if endpoint == "/v1/render" {
						field = "file"
					}
					files := []imageUpload{{field, base}}
					switch invalid {
					case "missing image":
						files = []imageUpload{{"mask", base}}
					case "mask size":
						files = append(files, imageUpload{"mask", small})
					case "markup size":
						files = append(files, imageUpload{"markup", small})
					case "invalid mask":
						files = append(files, imageUpload{"mask", []byte("broken")})
					case "invalid markup":
						files = append(files, imageUpload{"markup", []byte("broken")})
					}
					w := httptest.NewRecorder()
					s.ServeHTTP(w, imageRequest(t, endpoint, map[string]string{"model": "image-test", "prompt": "draw"}, files))
					if w.Code != 400 {
						t.Fatalf("response=%d %s", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}
