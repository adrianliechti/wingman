package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/adrianliechti/wingman/pkg/tool/custom"

	"google.golang.org/grpc"
)

func main() {
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", 50051))

	if err != nil {
		panic(err)
	}

	s := grpc.NewServer()
	custom.RegisterToolServer(s, newServer())
	s.Serve(l)
}

type server struct {
	custom.UnsafeToolServer
}

func newServer() *server {
	return &server{}
}

func (s *server) Tools(context.Context, *custom.ToolsRequest) (*custom.ToolsResponse, error) {
	schema, _ := json.Marshal(map[string]any{
		"type": "object",

		"properties": map[string]any{
			"args": map[string]any{
				"type": "array",

				"items": map[string]any{
					"type": "string",
				},
			},
		},

		"required": []string{"args"},
	})

	definition := custom.Definition{
		Name:        "kubectl",
		Description: "invoke the Kubernetes CLI kubectl with the given arguments",

		Parameters: string(schema),
	}

	return &custom.ToolsResponse{
		Definitions: []*custom.Definition{
			&definition,
		},
	}, nil
}

func (s *server) Execute(ctx context.Context, r *custom.ExecuteRequest) (*custom.ResultResponse, error) {
	if r.Name != "kubectl" {
		return nil, fmt.Errorf("unknown tool: %s", r.Name)
	}

	var input struct {
		Args []string `json:"args"`
	}

	if err := json.Unmarshal([]byte(r.Parameters), &input); err != nil {
		return nil, err
	}

	args := input.Args

	println("$ kubectl " + strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, "kubectl", args...)

	output, _ := cmd.CombinedOutput()

	return &custom.ResultResponse{
		Data: string(output),
	}, nil
}
