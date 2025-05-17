package tika_test

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/tika"
	"github.com/adrianliechti/wingman/pkg/provider"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestExtract(t *testing.T) {
	ctx := context.Background()

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "apache/tika:3.0.0.0-BETA2-full",
			ExposedPorts: []string{"9998/tcp"},
			WaitingFor:   wait.ForLog("Started Apache Tika server"),
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(ctx, "")
	require.NoError(t, err)

	c, err := tika.New("http://" + url)
	require.NoError(t, err)

	resp, err := http.Get("https://helpx.adobe.com/pdf/acrobat_reference.pdf")
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	input := extractor.Input{
		File: &provider.File{
			Content:     data,
			ContentType: "application/pdf",
		},
	}

	result, err := c.Extract(ctx, input, nil)
	require.NoError(t, err)

	require.NotEmpty(t, result.Content)
}
