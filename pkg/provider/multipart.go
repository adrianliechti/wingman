package provider

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

type MultipartPart struct {
	Header  textproto.MIMEHeader
	Content interface {
		io.ReaderAt
		Size() int64
	}
}

// NewMultipartRequest builds a POST with a known Content-Length and a
// replayable body. Each part is read directly from its original content.
func NewMultipartRequest(ctx context.Context, url string, parts ...MultipartPart) (*http.Request, error) {
	var envelope bytes.Buffer
	writer := multipart.NewWriter(&envelope)
	offsets := make([]int, 0, len(parts))
	var contentLength int64
	for _, part := range parts {
		if _, err := writer.CreatePart(part.Header); err != nil {
			return nil, err
		}
		offsets = append(offsets, envelope.Len())
		contentLength += part.Content.Size()
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	envelopeBytes := envelope.Bytes()
	newBody := func() io.ReadCloser {
		readers := make([]io.Reader, 0, len(parts)*2+1)
		start := 0
		for i, part := range parts {
			end := offsets[i]
			readers = append(readers, bytes.NewReader(envelopeBytes[start:end]), io.NewSectionReader(part.Content, 0, part.Content.Size()))
			start = end
		}
		readers = append(readers, bytes.NewReader(envelopeBytes[start:]))
		return io.NopCloser(io.MultiReader(readers...))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, newBody())
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = contentLength + int64(len(envelopeBytes))
	req.GetBody = func() (io.ReadCloser, error) { return newBody(), nil }
	return req, nil
}
