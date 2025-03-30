package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/config"
	"github.com/adrianliechti/wingman/pkg/client"
)

func main() {
	urlFlag := flag.String("url", "http://localhost:8080", "server url")
	tokenFlag := flag.String("token", "", "server token")
	modelFlag := flag.String("model", "", "model id")

	flag.Parse()

	ctx := context.Background()

	model := *modelFlag

	options := []client.RequestOption{}

	if *tokenFlag != "" {
		options = append(options, client.WithToken(*tokenFlag))
	}

	client := client.New(*urlFlag, options...)

	if model == "" {
		val, err := selectModel(ctx, client)

		if err != nil {
			panic(err)
		}

		model = val
	}

	if config.DetectModelType(model) == config.ModelTypeEmbedder {
		embed(ctx, client, model)
		return
	}

	if config.DetectModelType(model) == config.ModelTypeRenderer {
		panic("Image generation is not supported yet")
		//render(ctx, &client, model)
		//return
	}

	if config.DetectModelType(model) == config.ModelTypeSynthesizer {
		panic("Audio synthesis is not supported yet")
		//synthesize(ctx, &client, model)
		//return
	}

	chat(ctx, client, model)
}

func selectModel(ctx context.Context, client *client.Client) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	output := os.Stdout

	models, err := client.Models.List(ctx)

	if err != nil {
		return "", err
	}

	sort.SliceStable(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	for i, m := range models {
		output.WriteString(fmt.Sprintf("%2d) ", i+1))
		output.WriteString(m.ID)
		output.WriteString("\n")
	}

	output.WriteString(" >  ")
	sel, err := reader.ReadString('\n')

	if err != nil {
		panic(err)
	}

	idx, err := strconv.Atoi(strings.TrimSpace(sel))

	if err != nil {
		panic(err)
	}

	output.WriteString("\n")

	model := models[idx-1].ID
	return model, nil
}

func chat(ctx context.Context, c *client.Client, model string) {
	reader := bufio.NewReader(os.Stdin)
	output := os.Stdout

	req := client.CompletionRequest{
		Model: model,

		Messages: []client.Message{},

		CompleteOptions: client.CompleteOptions{
			Stream: func(ctx context.Context, completion client.Completion) error {
				output.WriteString(completion.Message.Text())
				return nil
			},
		},
	}

LOOP:
	for {
		output.WriteString(">>> ")
		input, err := reader.ReadString('\n')

		if err != nil {
			panic(err)
		}

		input = strings.TrimSpace(input)

		if strings.HasPrefix(input, "/") {
			switch strings.ToLower(input) {
			case "/reset":
				req.Messages = []client.Message{}
				continue LOOP

			default:
				output.WriteString("Unknown command\n")
				continue LOOP
			}
		}

		req.Messages = append(req.Messages, client.UserMessage(input))

		completion, err := c.Completions.New(ctx, req)

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		req.Messages = append(req.Messages, *completion.Message)

		output.WriteString("\n")
		output.WriteString("\n")
	}
}

func embed(ctx context.Context, c *client.Client, model string) {
	reader := bufio.NewReader(os.Stdin)
	output := os.Stdout

LOOP:
	for {
		output.WriteString(">>> ")
		input, err := reader.ReadString('\n')

		if err != nil {
			panic(err)
		}

		input = strings.TrimSpace(input)

		result, err := c.Embeddings.New(ctx, client.EmbeddingsRequest{
			Model: model,
			Texts: []string{input},
		})

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		for i, e := range result.Embeddings[0] {
			if i > 0 {
				output.WriteString(", ")
			}

			output.WriteString(fmt.Sprintf("%f", e))
		}

		output.WriteString("\n")
		output.WriteString("\n")
	}
}

// func render(ctx context.Context, client *openai.Client, model string) {
// 	reader := bufio.NewReader(os.Stdin)
// 	output := os.Stdout

// LOOP:
// 	for {
// 		output.WriteString(">>> ")
// 		input, err := reader.ReadString('\n')

// 		if err != nil {
// 			panic(err)
// 		}

// 		input = strings.TrimSpace(input)

// 		image, err := client.Images.Generate(ctx, openai.ImageGenerateParams{
// 			Model: openai.ImageModel(model),

// 			Prompt: input,

// 			ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
// 		})

// 		if err != nil {
// 			output.WriteString(err.Error() + "\n")
// 			continue LOOP
// 		}

// 		data, err := base64.StdEncoding.DecodeString(image.Data[0].B64JSON)

// 		if err != nil {
// 			output.WriteString(err.Error() + "\n")
// 			continue LOOP
// 		}

// 		name := uuid.New().String()

// 		if ext, _ := mime.ExtensionsByType(http.DetectContentType(data)); len(ext) > 0 {
// 			name += ext[0]
// 		}

// 		os.WriteFile(name, data, 0600)
// 		fmt.Println("Saved: " + name)

// 		output.WriteString("\n")
// 		output.WriteString("\n")
// 	}
// }

// func synthesize(ctx context.Context, client *openai.Client, model string) {
// 	reader := bufio.NewReader(os.Stdin)
// 	output := os.Stdout

// LOOP:
// 	for {
// 		output.WriteString(">>> ")
// 		input, err := reader.ReadString('\n')

// 		if err != nil {
// 			panic(err)
// 		}

// 		input = strings.TrimSpace(input)

// 		result, err := client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{
// 			Model: openai.SpeechModel(model),

// 			Input: input,

// 			Voice:          openai.AudioSpeechNewParamsVoiceAlloy,
// 			ResponseFormat: openai.AudioSpeechNewParamsResponseFormatWAV,
// 		})

// 		if err != nil {
// 			output.WriteString(err.Error() + "\n")
// 			continue LOOP
// 		}

// 		defer result.Body.Close()

// 		data, err := io.ReadAll(result.Body)

// 		if err != nil {
// 			output.WriteString(err.Error() + "\n")
// 			continue LOOP
// 		}

// 		name := uuid.New().String()

// 		if ext, _ := mime.ExtensionsByType(http.DetectContentType(data)); len(ext) > 0 {
// 			name += ext[0]
// 		} else {
// 			name += ".wav"
// 		}

// 		os.WriteFile(name, data, 0600)
// 		fmt.Println("Saved: " + name)

// 		output.WriteString("\n")
// 		output.WriteString("\n")
// 	}
// }
