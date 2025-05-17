package llama_test

import (
	"context"
	"testing"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/provider/llama"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCompleter(t *testing.T) {
	ctx := context.Background()

	server, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		Started: true,

		ContainerRequest: testcontainers.ContainerRequest{
			Image: "ghcr.io/ggml-org/llama.cpp:server",

			Cmd: []string{
				"--hf-repo", "bartowski/Phi-3.5-mini-instruct-GGUF",
				"--hf-file", "Phi-3.5-mini-instruct-Q4_K_M.gguf",
				"--ctx-size", "4096",
			},

			Mounts: testcontainers.Mounts(
				testcontainers.ContainerMount{
					Target: "/root/.cache/llama.cpp",
					Source: testcontainers.DockerVolumeMountSource{
						Name: "llama",
					},
				},
			),

			ExposedPorts: []string{"8080/tcp"},

			WaitingFor: wait.ForLog("starting the main loop"),
		},
	})

	require.NoError(t, err)

	url, err := server.Endpoint(ctx, "")
	require.NoError(t, err)

	c, err := llama.NewCompleter("http://"+url, "default")
	require.NoError(t, err)

	result, err := c.Complete(ctx, []provider.Message{
		provider.UserMessage("Hello!"),
	}, nil)

	require.NoError(t, err)
	require.NotEmpty(t, result.Message.Content)

	t.Log(result.Message.Content)
}
