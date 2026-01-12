package kreuzberg_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/kreuzberg"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestExtract(t *testing.T) {
	ctx := context.Background()

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "goldziher/kreuzberg:4.0.0-core",
			ExposedPorts: []string{"8000/tcp"},
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(ctx, "")
	require.NoError(t, err)

	c, err := kreuzberg.New("http://" + url)

	require.NoError(t, err)

	resp, err := http.Get("https://www.adobe.com/support/products/enterprise/knowledgecenter/media/c4611_sample_explain.pdf")
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	input := extractor.File{
		Name: "acrobat_reference.pdf",

		Content:     data,
		ContentType: "application/pdf",
	}

	result, err := c.Extract(ctx, input, nil)
	require.NoError(t, err)

	require.NotEmpty(t, result.Text)
}
