package jina_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/extractor"
	"github.com/adrianliechti/wingman/pkg/extractor/jina"
	"github.com/adrianliechti/wingman/pkg/to"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestExtract(t *testing.T) {
	ctx := context.Background()

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/adrianliechti/wingman-reader",
			ExposedPorts: []string{"8080/tcp"},
			WaitingFor:   wait.ForExposedPort(),
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(ctx, "")
	require.NoError(t, err)

	c, err := jina.New("http://" + url)
	require.NoError(t, err)

	input := extractor.Input{
		URL: to.Ptr("https://example.org"),
	}

	result, err := c.Extract(ctx, input, nil)
	require.NoError(t, err)

	require.NotEmpty(t, result.Content)
}
