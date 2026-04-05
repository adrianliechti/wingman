package text

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestCodeSplitter_Go(t *testing.T) {
	source := `package main

import "fmt"

// Greeting returns a greeting message.
func Greeting(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}

// Farewell returns a farewell message.
func Farewell(name string) string {
	return fmt.Sprintf("Goodbye, %s!", name)
}

func main() {
	fmt.Println(Greeting("World"))
	fmt.Println(Farewell("World"))
}
`
	splitter := NewCodeSplitter("main.go")
	assert.NotNil(t, splitter)

	splitter.ChunkSize = 100

	chunks := splitter.Split(source)
	assert.True(t, len(chunks) > 1, "should split into multiple chunks, got %d", len(chunks))

	for _, chunk := range chunks {
		assert.NotEmpty(t, chunk)
		t.Logf("chunk (%d chars):\n%s\n---", len(chunk), chunk)
	}
}

func TestCodeSplitter_Python(t *testing.T) {
	source := `import os
import sys

class Calculator:
    def __init__(self):
        self.result = 0

    def add(self, a, b):
        return a + b

    def subtract(self, a, b):
        return a - b

def main():
    calc = Calculator()
    print(calc.add(1, 2))
    print(calc.subtract(5, 3))

if __name__ == "__main__":
    main()
`
	splitter := NewCodeSplitter("calculator.py")
	assert.NotNil(t, splitter)

	splitter.ChunkSize = 120

	chunks := splitter.Split(source)
	assert.True(t, len(chunks) > 1, "should split into multiple chunks")

	for _, chunk := range chunks {
		assert.True(t, len(chunk) > 0, "chunks should not be empty")
	}
}

func TestCodeSplitter_SmallFile(t *testing.T) {
	source := `package main

func main() {}
`
	splitter := NewCodeSplitter("main.go")
	assert.NotNil(t, splitter)

	chunks := splitter.Split(source)
	assert.Equal(t, 1, len(chunks), "small file should be a single chunk")
}

func TestCodeSplitter_UnsupportedLanguage(t *testing.T) {
	splitter := NewCodeSplitter("data.xyz123")
	assert.Nil(t, splitter, "unsupported extension should return nil")
}

func TestCodeSplitter_JavaScript(t *testing.T) {
	source := `const express = require('express');

function handleRequest(req, res) {
    const data = req.body;
    if (!data) {
        return res.status(400).json({ error: 'No data' });
    }
    return res.json({ success: true });
}

class UserService {
    constructor(db) {
        this.db = db;
    }

    async getUser(id) {
        return await this.db.findOne({ id });
    }

    async createUser(data) {
        return await this.db.create(data);
    }
}

module.exports = { handleRequest, UserService };
`
	splitter := NewCodeSplitter("app.js")
	assert.NotNil(t, splitter)

	splitter.ChunkSize = 150

	chunks := splitter.Split(source)
	assert.True(t, len(chunks) > 1, "should split into multiple chunks")

	for _, chunk := range chunks {
		assert.True(t, len(chunk) > 0, "chunks should not be empty")
	}
}

func TestCodeSplitter_WithOverlap(t *testing.T) {
	source := `package main

func A() string {
	return "a"
}

func B() string {
	return "b"
}

func C() string {
	return "c"
}

func D() string {
	return "d"
}
`
	splitter := NewCodeSplitter("main.go")
	assert.NotNil(t, splitter)

	splitter.ChunkSize = 60
	splitter.ChunkOverlap = 20

	chunks := splitter.Split(source)
	assert.True(t, len(chunks) > 1, "should split into multiple chunks")

	for _, chunk := range chunks {
		assert.True(t, len(chunk) > 0, "chunks should not be empty")
	}
}

// --- Integration tests (Redis server.c, OpenJDK HashMap.java) ---

func TestCodeSplitter_Redis_C(t *testing.T) {
	source := fetchText(t, "https://raw.githubusercontent.com/redis/redis/unstable/src/server.c")
	t.Logf("Input: %d chars, %d bytes, %d lines", utf8.RuneCountInString(source), len(source), strings.Count(source, "\n"))

	splitter := NewCodeSplitter("server.c")
	if splitter == nil {
		t.Fatal("expected C language to be supported")
	}

	for _, chunkSize := range []int{500, 1000, 1500} {
		t.Run("", func(t *testing.T) {
			splitter.ChunkSize = chunkSize
			splitter.ChunkOverlap = 0

			chunks := splitter.Split(source)
			t.Logf("ChunkSize=%d → %d chunks", chunkSize, len(chunks))

			for i, chunk := range chunks {
				runeLen := utf8.RuneCountInString(chunk)
				if runeLen > chunkSize {
					t.Errorf("chunk %d exceeds size: %d > %d\npreview: %q", i, runeLen, chunkSize, truncate(chunk, 100))
				}
				if strings.TrimSpace(chunk) == "" {
					t.Errorf("chunk %d is empty", i)
				}
			}

			if len(chunks) > 0 {
				t.Logf("first: %q", truncate(chunks[0], 100))
				t.Logf("last:  %q", truncate(chunks[len(chunks)-1], 100))
			}
		})
	}
}

func TestCodeSplitter_Redis_C_WithOverlap(t *testing.T) {
	source := fetchText(t, "https://raw.githubusercontent.com/redis/redis/unstable/src/server.c")

	splitter := NewCodeSplitter("server.c")
	if splitter == nil {
		t.Fatal("expected C language to be supported")
	}

	splitter.ChunkSize = 1000
	splitter.ChunkOverlap = 200

	chunks := splitter.Split(source)
	t.Logf("ChunkSize=1000, Overlap=200 → %d chunks", len(chunks))

	for i, chunk := range chunks {
		runeLen := utf8.RuneCountInString(chunk)
		if runeLen > splitter.ChunkSize {
			t.Errorf("chunk %d exceeds size: %d > %d", i, runeLen, splitter.ChunkSize)
		}
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}

	overlapCount := 0
	totalOverlapChars := 0
	for i := 1; i < len(chunks); i++ {
		if found := findOverlap(chunks[i-1], chunks[i]); found != "" {
			overlapCount++
			totalOverlapChars += utf8.RuneCountInString(found)
		}
	}

	pct := float64(overlapCount) / float64(len(chunks)-1) * 100
	avgOverlap := 0
	if overlapCount > 0 {
		avgOverlap = totalOverlapChars / overlapCount
	}
	t.Logf("Overlapping pairs: %d/%d (%.0f%%), avg overlap: %d chars",
		overlapCount, len(chunks)-1, pct, avgOverlap)

	if pct < 80 {
		t.Errorf("Too few overlapping pairs: %.0f%% (expected >80%%)", pct)
	}
}

func TestCodeSplitter_HashMap_Java(t *testing.T) {
	source := fetchText(t, "https://raw.githubusercontent.com/openjdk/jdk/master/src/java.base/share/classes/java/util/HashMap.java")
	t.Logf("Input: %d chars, %d bytes, %d lines", utf8.RuneCountInString(source), len(source), strings.Count(source, "\n"))

	splitter := NewCodeSplitter("HashMap.java")
	if splitter == nil {
		t.Fatal("expected Java language to be supported")
	}

	for _, chunkSize := range []int{500, 1000, 1500} {
		t.Run("", func(t *testing.T) {
			splitter.ChunkSize = chunkSize
			splitter.ChunkOverlap = 0

			chunks := splitter.Split(source)
			t.Logf("ChunkSize=%d → %d chunks", chunkSize, len(chunks))

			for i, chunk := range chunks {
				runeLen := utf8.RuneCountInString(chunk)
				if runeLen > chunkSize {
					t.Errorf("chunk %d exceeds size: %d > %d\npreview: %q", i, runeLen, chunkSize, truncate(chunk, 100))
				}
				if strings.TrimSpace(chunk) == "" {
					t.Errorf("chunk %d is empty", i)
				}
			}

			if len(chunks) > 0 {
				t.Logf("first: %q", truncate(chunks[0], 100))
				t.Logf("last:  %q", truncate(chunks[len(chunks)-1], 100))
			}
		})
	}
}

func TestCodeSplitter_HashMap_Java_WithOverlap(t *testing.T) {
	source := fetchText(t, "https://raw.githubusercontent.com/openjdk/jdk/master/src/java.base/share/classes/java/util/HashMap.java")

	splitter := NewCodeSplitter("HashMap.java")
	if splitter == nil {
		t.Fatal("expected Java language to be supported")
	}

	splitter.ChunkSize = 1000
	splitter.ChunkOverlap = 200

	chunks := splitter.Split(source)
	t.Logf("ChunkSize=1000, Overlap=200 → %d chunks", len(chunks))

	for i, chunk := range chunks {
		runeLen := utf8.RuneCountInString(chunk)
		if runeLen > splitter.ChunkSize {
			t.Errorf("chunk %d exceeds size: %d > %d", i, runeLen, splitter.ChunkSize)
		}
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("chunk %d is empty", i)
		}
	}

	overlapCount := 0
	totalOverlapChars := 0
	for i := 1; i < len(chunks); i++ {
		if found := findOverlap(chunks[i-1], chunks[i]); found != "" {
			overlapCount++
			totalOverlapChars += utf8.RuneCountInString(found)
		}
	}

	pct := float64(overlapCount) / float64(len(chunks)-1) * 100
	avgOverlap := 0
	if overlapCount > 0 {
		avgOverlap = totalOverlapChars / overlapCount
	}
	t.Logf("Overlapping pairs: %d/%d (%.0f%%), avg overlap: %d chars",
		overlapCount, len(chunks)-1, pct, avgOverlap)

	if pct < 80 {
		t.Errorf("Too few overlapping pairs: %.0f%% (expected >80%%)", pct)
	}
}
