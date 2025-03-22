package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/adrianliechti/wingman/pkg/client"
	"github.com/adrianliechti/wingman/pkg/to"

	"github.com/openai/openai-go"
)

var (
	urlFlag   = flag.String("url", "http://localhost:8080", "platform url")
	tokenFlag = flag.String("token", "", "platform token")

	indexFlag = flag.String("index", "", "index name")
	dirFlag   = flag.String("dir", ".", "index directory")

	embeddingFlag = flag.String("embedding", "", "embedding model")
)

func main() {
	flag.Parse()

	ctx := context.Background()

	opts := []client.RequestOption{}

	if *tokenFlag != "" {
		opts = append(opts, client.WithToken(*tokenFlag))
	}

	c := client.New(*urlFlag, opts...)

	if err := IndexDir(ctx, c, *indexFlag, *dirFlag); err != nil {
		panic(err)
	}
}

func IndexDir(ctx context.Context, c *client.Client, index, root string) error {
	supported := []string{
		".csv",
		".md",
		".rst",
		".tsv",
		".txt",

		".pdf",

		// ".jpg", ".jpeg",
		// ".png",
		// ".bmp",
		// ".tiff",
		// ".heif",

		".docx",
		".pptx",
		".xlsx",
	}

	var result error

	revisions := map[string]string{}

	filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		if strings.Contains(path, ".cache") {
			return nil
		}

		if e.IsDir() || !slices.Contains(supported, filepath.Ext(path)) {
			return nil
		}

		data, err := os.ReadFile(path)

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		md5_hash := md5.Sum(data)
		md5_text := hex.EncodeToString(md5_hash[:])

		cachedir := filepath.Join(root, ".cache", md5_text[0:2], md5_text[2:4], md5_text)
		os.MkdirAll(cachedir, 0755)

		info, err := e.Info()

		if err != nil {
			result = errors.Join(result, err)
			return nil
		}

		rel, _ := filepath.Rel(root, path)

		name := filepath.Base(path)
		title := strings.TrimSuffix(name, filepath.Ext(name))
		revision := md5_text

		metadata := Metadata{
			Name: filepath.Base(path),
			Path: "/" + rel,

			Title:    title,
			Revision: revision,

			Size: info.Size(),
			Time: info.ModTime(),
		}

		//if !exists(cachedir, "metadata.json") {
		if err := writeJSON(cachedir, "metadata.json", metadata); err != nil {
			result = errors.Join(result, err)
			return nil
		}
		//}

		if !exists(cachedir, "content.txt") {
			body := client.ExtractionRequest{
				Name:   metadata.Name,
				Reader: bytes.NewReader(data),
			}

			content, err := c.Extractions.New(ctx, body)

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			if err := writeData(cachedir, "content.txt", []byte(content.Text)); err != nil {
				result = errors.Join(result, err)
				return nil
			}
		}

		if *embeddingFlag != "" && !exists(cachedir, "embeddings.json") {
			text, err := readText(cachedir, "content.txt")

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			segments, err := c.Segments.New(ctx, client.SegmentRequest{
				Text: text,

				SegmentLength:  to.Ptr(3000),
				SegmentOverlap: to.Ptr(1500),
			})

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			embeddings := Embeddings{
				Model: *embeddingFlag,
			}

			titleEmbedding, err := c.Embeddings.New(ctx, openai.EmbeddingNewParams{
				Model: openai.EmbeddingModel(*embeddingFlag),
				Input: openai.EmbeddingNewParamsInputUnion{
					OfArrayOfStrings: []string{title},
				},
			})

			if err != nil {
				result = errors.Join(result, err)
				return nil
			}

			embeddings.Segments = append(embeddings.Segments, Segment{
				Text:      title,
				Embedding: toFloat32(titleEmbedding.Data[0].Embedding),
			})

			for _, segment := range segments {
				segmentEmbedding, err := c.Embeddings.New(ctx, openai.EmbeddingNewParams{
					Model: openai.EmbeddingModel(*embeddingFlag),
					Input: openai.EmbeddingNewParamsInputUnion{
						OfString: openai.String(segment.Text),
					},
				})

				if err != nil {
					result = errors.Join(result, err)
					return nil
				}

				embeddings.Segments = append(embeddings.Segments, Segment{
					Text:      segment.Text,
					Embedding: toFloat32(segmentEmbedding.Data[0].Embedding),
				})
			}

			if err := writeJSON(cachedir, "embeddings.json", embeddings); err != nil {
				result = errors.Join(result, err)
				return nil
			}
		}

		if index != "" && !exists(cachedir, "documents.json") {
			var embeddings Embeddings

			if err := readJSON(cachedir, "embeddings.json", &embeddings); err != nil {
				result = errors.Join(result, err)
				return nil
			}

			var documents []client.Document

			for i, segment := range embeddings.Segments {
				document := client.Document{
					Title:  metadata.Title,
					Source: fmt.Sprintf("%s#%d", metadata.Path, i+1),

					Content:   segment.Text,
					Embedding: segment.Embedding,

					Metadata: map[string]string{
						"filename": metadata.Name,
						"filepath": metadata.Path,

						"index":    fmt.Sprintf("%d", i),
						"revision": metadata.Revision,
					},
				}

				if _, err := c.Documents.New(ctx, index, []client.Document{document}); err != nil {
					result = errors.Join(result, err)
					return nil
				}

				documents = append(documents, document)
			}

			if err != writeJSON(cachedir, "documents.json", documents) {
				result = errors.Join(result, err)
				return nil
			}
		}

		revisions[metadata.Path] = metadata.Revision

		println(metadata.Path, metadata.Revision)

		return nil
	})

	if index != "" {
		list, err := c.Documents.List(ctx, index)

		if err != nil {
			return err
		}

		var deletions []string

		for _, d := range list {
			filepath := d.Metadata["filepath"]
			revision := d.Metadata["revision"]

			if filepath == "" || revision == "" {
				continue
			}

			ref := revisions[filepath]

			if strings.EqualFold(revision, ref) {
				continue
			}

			deletions = append(deletions, d.ID)
		}

		if len(deletions) > 0 {
			if err := c.Documents.Delete(ctx, index, deletions); err != nil {
				return err
			}
		}
	}

	return result
}

type Metadata struct {
	Name string `json:"name"`
	Path string `json:"path"`

	Title string `json:"title"`

	Revision string `json:"revision"`

	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

type Embeddings struct {
	Model string `json:"model"`

	Segments []Segment `json:"segments"`
}

type Segment struct {
	Text string `json:"text"`

	Embedding []float32 `json:"embedding"`
}

func exists(path, name string) bool {
	info, err := os.Stat(filepath.Join(path, name))

	if err != nil {
		if os.IsNotExist(err) {
			return false
		}

		return false
	}

	return !info.IsDir()
}

func readData(dir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, name))
}

func readText(dir, name string) (string, error) {
	data, err := readData(dir, name)

	if err != nil {
		return "", err
	}

	return string(data), nil
}

func readJSON(dir, name string, v any) error {
	data, err := readData(dir, name)

	if err != nil {
		return err
	}

	return json.Unmarshal(data, v)
}

func writeData(dir, name string, data []byte) error {
	return os.WriteFile(filepath.Join(dir, name), data, 0644)
}

func writeJSON(dir, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")

	if err != nil {
		return err
	}

	return writeData(dir, name, data)
}

func toFloat32(input []float64) []float32 {
	result := make([]float32, len(input))

	for i, v := range input {
		result[i] = float32(v)
	}

	return result
}
