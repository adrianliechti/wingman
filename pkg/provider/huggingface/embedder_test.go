package huggingface_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider/huggingface"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestEmbedder(t *testing.T) {
	ctx := context.Background()

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image:         "ghcr.io/huggingface/text-embeddings-inference:cpu-1.6",
			ImagePlatform: "linux/amd64",

			Cmd: []string{"--model-id", "BAAI/bge-large-en-v1.5"},

			Mounts: testcontainers.Mounts(
				testcontainers.ContainerMount{
					Target: "/data",
					Source: testcontainers.DockerVolumeMountSource{
						Name: "huggingface",
					},
				},
			),

			ExposedPorts: []string{"80/tcp"},

			WaitingFor: wait.ForLog("Ready"),
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(ctx, "")
	require.NoError(t, err)

	e, err := huggingface.NewEmbedder("http://"+url, "")
	require.NoError(t, err)

	result, err := e.Embed(ctx, []string{"Hello, World!", "Hello Welt!"}, nil)
	require.NoError(t, err)

	require.Len(t, result.Embeddings, 2)

	require.NotEmpty(t, result.Embeddings[0])
	require.NotEmpty(t, result.Embeddings[1])
}
