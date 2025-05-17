package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/provider/custom"
	"github.com/google/uuid"

	"google.golang.org/grpc"
)

func main() {
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", 50051))

	if err != nil {
		panic(err)
	}

	s := grpc.NewServer()
	custom.RegisterCompleterServer(s, newServer())
	s.Serve(l)
}

type server struct {
	custom.UnsafeCompleterServer
}

func newServer() *server {
	return &server{}
}

func (s *server) Complete(r *custom.CompleteRequest, stream grpc.ServerStreamingServer[custom.Completion]) error {
	text := "Please provide me more information about the topic."

	words := strings.Split(text, " ")

	for _, word := range words {
		content := word + " "

		time.Sleep(300 * time.Millisecond)

		stream.Send(&custom.Completion{
			Id:    uuid.NewString(),
			Model: "test",

			Delta: &custom.Message{
				Role: "assistant",

				Content: []*custom.Content{
					{
						Text: &content,
					},
				},
			},
		})
	}

	stream.Send(&custom.Completion{
		Id:    uuid.NewString(),
		Model: "test",

		Message: &custom.Message{
			Role: "assistant",

			Content: []*custom.Content{
				{
					Text: &text,
				},
			},
		},
	})

	return nil
}
