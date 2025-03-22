package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/adrianliechti/wingman/config"

	"github.com/google/uuid"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	urlFlag := flag.String("url", "http://localhost:8080/v1", "server url")
	tokenFlag := flag.String("token", "", "server token")
	modelFlag := flag.String("model", "", "model id")

	flag.Parse()

	ctx := context.Background()

	options := []option.RequestOption{}

	if *urlFlag != "" {
		url := *urlFlag
		url = strings.TrimRight(url, "/") + "/"

		options = append(options, option.WithBaseURL(url))
	}

	if *tokenFlag != "" {
		options = append(options, option.WithAPIKey(*tokenFlag))
	}

	client := openai.NewClient(options...)
	model := *modelFlag

	if model == "" {
		val, err := selectModel(ctx, &client)

		if err != nil {
			panic(err)
		}

		model = val
	}

	if config.DetectModelType(model) == config.ModelTypeEmbedder {
		embed(ctx, &client, model)
		return
	}

	if config.DetectModelType(model) == config.ModelTypeRenderer {
		render(ctx, &client, model)
		return
	}

	if config.DetectModelType(model) == config.ModelTypeSynthesizer {
		synthesize(ctx, &client, model)
		return
	}

	chat(ctx, &client, model)
}

func selectModel(ctx context.Context, client *openai.Client) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	output := os.Stdout

	page := client.Models.ListAutoPaging(ctx)

	var models []openai.Model

	for page.Next() {
		model := page.Current()
		models = append(models, model)
	}

	if err := page.Err(); err != nil {
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

func chat(ctx context.Context, client *openai.Client, model string) {
	reader := bufio.NewReader(os.Stdin)
	output := os.Stdout

	var messages []openai.ChatCompletionMessageParamUnion

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
				messages = nil
				continue LOOP

			default:
				output.WriteString("Unknown command\n")
				continue LOOP
			}
		}

		messages = append(messages, openai.UserMessage(input))

		stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModel(model),
			Messages: messages,

			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: openai.Bool(true),
			},
		})

		completion := openai.ChatCompletionAccumulator{}

		for stream.Next() {
			chunk := stream.Current()
			completion.AddChunk(chunk)

			if len(chunk.Choices) > 0 {
				output.WriteString(chunk.Choices[0].Delta.Content)
			}
		}

		if err := stream.Err(); err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		messages = append(messages, completion.Choices[0].Message.ToParam())

		output.WriteString("\n")
		output.WriteString("\n")
	}
}

func embed(ctx context.Context, client *openai.Client, model string) {
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

		result, err := client.Embeddings.New(ctx, openai.EmbeddingNewParams{
			Model: openai.EmbeddingModel(model),

			Input: openai.EmbeddingNewParamsInputUnion{
				OfString: openai.String(input),
			},

			EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat,
		})

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		embedding := result.Data[0].Embedding

		for i, e := range embedding {
			if i > 0 {
				output.WriteString(", ")
			}

			output.WriteString(fmt.Sprintf("%f", e))
		}

		output.WriteString("\n")
		output.WriteString("\n")
	}
}

func render(ctx context.Context, client *openai.Client, model string) {
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

		image, err := client.Images.Generate(ctx, openai.ImageGenerateParams{
			Model: openai.ImageModel(model),

			Prompt: input,

			ResponseFormat: openai.ImageGenerateParamsResponseFormatB64JSON,
		})

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		data, err := base64.StdEncoding.DecodeString(image.Data[0].B64JSON)

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		name := uuid.New().String()

		if ext, _ := mime.ExtensionsByType(http.DetectContentType(data)); len(ext) > 0 {
			name += ext[0]
		}

		os.WriteFile(name, data, 0600)
		fmt.Println("Saved: " + name)

		output.WriteString("\n")
		output.WriteString("\n")
	}
}

func synthesize(ctx context.Context, client *openai.Client, model string) {
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

		result, err := client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{
			Model: openai.SpeechModel(model),

			Input: input,

			Voice:          openai.AudioSpeechNewParamsVoiceAlloy,
			ResponseFormat: openai.AudioSpeechNewParamsResponseFormatWAV,
		})

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		defer result.Body.Close()

		data, err := io.ReadAll(result.Body)

		if err != nil {
			output.WriteString(err.Error() + "\n")
			continue LOOP
		}

		name := uuid.New().String()

		if ext, _ := mime.ExtensionsByType(http.DetectContentType(data)); len(ext) > 0 {
			name += ext[0]
		} else {
			name += ".wav"
		}

		os.WriteFile(name, data, 0600)
		fmt.Println("Saved: " + name)

		output.WriteString("\n")
		output.WriteString("\n")
	}
}
