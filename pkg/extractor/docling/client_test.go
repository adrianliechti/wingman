package docling

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/adrianliechti/wingman/pkg/extractor"
)

func TestExtractAsyncProtocol(t *testing.T) {
	var bodies []*trackedBody
	polls := 0
	client, err := New("https://docling.test/", WithToken("key"), WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		for _, body := range bodies {
			if !body.closed {
				t.Error("previous response body remained open")
			}
		}
		if req.Header.Get("X-Api-Key") != "key" {
			t.Errorf("missing API key on %s", req.URL.Path)
		}
		response := ""
		switch req.URL.Path {
		case "/v1/convert/file/async":
			if req.Method != http.MethodPost {
				t.Errorf("submission method = %s", req.Method)
			}
			reader, err := req.MultipartReader()
			if err != nil {
				t.Fatal(err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() != "files" || part.FileName() != "report.pdf" || string(data) != "input" {
				t.Error("invalid file upload")
			}
			response = `{"task_id":"job","task_status":"pending"}`
		case "/v1/status/poll/job":
			if req.Method != http.MethodGet {
				t.Errorf("poll method = %s", req.Method)
			}
			response = []string{`{"task_status":"pending"}`, `{"task_status":"started"}`, `{"task_status":"success"}`}[polls]
			polls++
		case "/v1/result/job":
			if req.Method != http.MethodGet {
				t.Errorf("result method = %s", req.Method)
			}
			response = `{"status":"success","document":{"md_content":"# extracted text","json_content":{"name":"report"}}}`
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		body := &trackedBody{Reader: strings.NewReader(response)}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	client.pollInterval = time.Millisecond
	result, err := client.Extract(context.Background(), extractor.File{Name: "report.pdf", Content: []byte("input")}, nil)
	if err != nil || result.Text != "# extracted text" || polls != 3 {
		t.Fatalf("result = %+v, error = %v, polls = %d", result, err, polls)
	}
	for _, body := range bodies {
		if !body.closed {
			t.Error("response body not closed")
		}
	}
}

func TestExtractResponseErrors(t *testing.T) {
	for _, tt := range []struct {
		name, stage, response, want string
		status                      int
	}{
		{"submission HTTP error", "submit", `permission denied`, "permission denied", http.StatusForbidden},
		{"missing task ID", "submit", `{}`, "missing task ID", http.StatusOK},
		{"poll HTTP error", "poll", `job missing`, "job missing", http.StatusNotFound},
		{"failed task", "poll", `{"task_status":"failure"}`, "failure", http.StatusOK},
		{"result HTTP error", "result", `result missing`, "result missing", http.StatusNotFound},
		{"missing document", "result", `{"status":"success"}`, "missing document", http.StatusOK},
		{"failed conversion", "result", `{"status":"failure","document":{"md_content":"partial"}}`, "not successful", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var bodies []*trackedBody
			client, err := New("https://docling.test", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				stage, response := "submit", `{"task_id":"job"}`
				if strings.Contains(req.URL.Path, "/poll/") {
					stage, response = "poll", `{"task_status":"success"}`
				}
				if strings.Contains(req.URL.Path, "/result/") {
					stage, response = "result", `{"status":"success","document":{"md_content":"text"}}`
				}
				status := http.StatusOK
				if stage == tt.stage {
					response, status = tt.response, tt.status
				}
				body := &trackedBody{Reader: strings.NewReader(response)}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Body: body}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Extract(context.Background(), extractor.File{Name: "report.pdf"}, nil)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %s", err, tt.want)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("error response body remained open")
				}
			}
		})
	}
}

func TestAwaitTaskCancellationClosesPollResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &trackedBody{Reader: strings.NewReader(`{"task_status":"pending"}`)}
	client, err := New("https://docling.test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		cancel()
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := client.awaitTask(ctx, "job"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if !body.closed || time.Since(start) > time.Second {
		t.Fatal("polling did not promptly release resources on cancellation")
	}
}

func TestReadDocumentJSONContent(t *testing.T) {
	for _, content := range []string{`{"name":"report"}`, `"{\"name\":\"report\"}"`} {
		client, err := New("https://docling.test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"success","document":{"json_content":` + content + `}}`))}, nil
		})}))
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.readDocument(context.Background(), "job")
		if err != nil || result.Text != `{"name":"report"}` {
			t.Fatalf("result = %+v, error = %v", result, err)
		}
	}
}

func TestExtractMediaTypeWithoutFilename(t *testing.T) {
	client, err := New("https://docling.test", WithClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		reader, err := req.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		if part.FileName() != "document.pdf" || part.Header.Get("Content-Type") != "application/pdf" {
			t.Errorf("upload headers = %v", part.Header)
		}
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader("stop after checking upload"))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Extract(context.Background(), extractor.File{ContentType: "Application/PDF; charset=binary", Content: []byte("input")}, nil)
	if err == nil || !strings.Contains(err.Error(), "stop after checking upload") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadDocumentPrefersHTMLToEmptyJSON(t *testing.T) {
	client, err := New("https://docling.test", WithClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":"success","document":{"html_content":"<p>text</p>","json_content":{}}}`))}, nil
	})}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.readDocument(context.Background(), "job")
	if err != nil || result.Text != "<p>text</p>" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }
