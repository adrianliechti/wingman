package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"

	"github.com/adrianliechti/wingman/pkg/provider"
	"github.com/adrianliechti/wingman/pkg/router/classifier"
)

type candidateConfig struct {
	Model         string   `json:"model"`
	Card          string   `json:"card,omitempty"`
	Cost          float64  `json:"cost"`
	MaxDifficulty int      `json:"max_difficulty"`
	Vision        bool     `json:"vision"`
	MaxContext    int      `json:"max_context"`
	Examples      []string `json:"examples,omitempty"`
}

type noOpCompleter struct{}

func (noOpCompleter) Complete(context.Context, []provider.Message, *provider.CompleteOptions) iter.Seq2[*provider.Completion, error] {
	return func(func(*provider.Completion, error) bool) {}
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "router-replay:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("router-replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	candidatePath := flags.String("candidates", "", "JSON file containing classifier candidates")
	inputPath := flags.String("input", "", "JSONL file containing replay cases")
	defaultModel := flags.String("default", "", "default candidate model (first candidate when omitted)")
	details := flags.Bool("details", false, "include per-case decisions in output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *candidatePath == "" || *inputPath == "" {
		return errors.New("-candidates and -input are required")
	}

	candidateFile, err := os.Open(*candidatePath)
	if err != nil {
		return err
	}
	defer candidateFile.Close()

	var configs []candidateConfig
	decoder := json.NewDecoder(candidateFile)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configs); err != nil {
		return fmt.Errorf("decode candidates: %w", err)
	}

	candidates := make([]classifier.Candidate, 0, len(configs))
	defaultIndex := 0
	defaultFound := *defaultModel == ""
	for i, config := range configs {
		candidates = append(candidates, classifier.Candidate{
			Completer: noOpCompleter{},
			Model:     config.Model, Card: config.Card, Cost: config.Cost,
			MaxDifficulty: config.MaxDifficulty, Vision: config.Vision,
			MaxContext: config.MaxContext, Examples: config.Examples,
		})
		if config.Model == *defaultModel {
			defaultIndex = i
			defaultFound = true
		}
	}
	if !defaultFound {
		return fmt.Errorf("default model %q is not a candidate", *defaultModel)
	}

	router, err := classifier.NewCompleter(candidates, classifier.Options{DefaultIndex: defaultIndex})
	if err != nil {
		return err
	}

	inputFile, err := os.Open(*inputPath)
	if err != nil {
		return err
	}
	defer inputFile.Close()

	var cases []classifier.ReplayCase
	scanner := bufio.NewScanner(inputFile)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		var replayCase classifier.ReplayCase
		if err := json.Unmarshal(scanner.Bytes(), &replayCase); err != nil {
			return fmt.Errorf("decode input line %d: %w", line, err)
		}
		cases = append(cases, replayCase)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	report := router.Replay(context.Background(), cases)
	if !*details {
		report.Results = nil
	}

	output := json.NewEncoder(stdout)
	output.SetIndent("", "  ")
	return output.Encode(report)
}
