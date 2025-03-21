package qdrant_test

import (
	"testing"

	"github.com/adrianliechti/wingman/pkg/index/qdrant"
	"github.com/adrianliechti/wingman/test"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestQdrant(t *testing.T) {
	context := test.NewContext()

	server, err := testcontainers.GenericContainer(context.Context, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "qdrant/qdrant:v1.13.3",
			ExposedPorts: []string{"6333/tcp"},
			WaitingFor:   wait.ForLog("Qdrant HTTP listening on 6333"),
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(context.Context, "")
	require.NoError(t, err)

	c, err := qdrant.New("http://"+url, "test", qdrant.WithEmbedder(context.Embedder))

	if err != nil {
		t.Fatal(err)
	}

	test.TestIndex(t, context, c)
}
