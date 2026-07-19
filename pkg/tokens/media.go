package tokens

import (
	"bytes"
	"encoding/binary"
	"image"
	"math"
	"regexp"
	"strings"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// ImageDims returns the pixel dimensions of raw image bytes (PNG, JPEG, GIF,
// WebP). ok is false when the format is unrecognized.
func ImageDims(data []byte) (w, h int, ok bool) {
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		return cfg.Width, cfg.Height, true
	}
	return webpDims(data)
}

// webpDims parses WebP canvas dimensions (stdlib image has no WebP decoder).
func webpDims(data []byte) (int, int, bool) {
	if len(data) < 30 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, false
	}
	switch string(data[12:16]) {
	case "VP8X": // extended: 24-bit canvas minus one at offsets 24/27
		w := int(uint32(data[24]) | uint32(data[25])<<8 | uint32(data[26])<<16)
		h := int(uint32(data[27]) | uint32(data[28])<<8 | uint32(data[29])<<16)
		return w + 1, h + 1, true
	case "VP8 ": // lossy: dims at offset 26, 14 bits each
		w := int(binary.LittleEndian.Uint16(data[26:28]) & 0x3FFF)
		h := int(binary.LittleEndian.Uint16(data[28:30]) & 0x3FFF)
		return w, h, true
	case "VP8L": // lossless: packed 14-bit dims after signature byte
		bits := binary.LittleEndian.Uint32(data[21:25])
		return int(bits&0x3FFF) + 1, int((bits>>14)&0x3FFF) + 1, true
	}
	return 0, 0, false
}

// Claude image costing (measured against count_tokens and per
// platform.claude.com/docs vision guide): 28x28-pixel patches,
// tokens = ceil(w/28)*ceil(h/28), after downscaling to the family's long-edge
// and visual-token caps.
func claudeImageCaps(family Family) (longEdge, maxTokens int) {
	if family == Claude2026 {
		return 2576, 4784
	}
	return 1568, 1568
}

// ClaudeImage estimates image tokens for a Claude model given pixel
// dimensions. Unknown dimensions (w or h ≤ 0) cost the family cap —
// conservative for budgeting.
func ClaudeImage(model string, w, h int) int {
	family := FamilyFor(model)
	longEdge, maxTokens := claudeImageCaps(family)
	if w <= 0 || h <= 0 {
		return maxTokens
	}
	fw, fh := float64(w), float64(h)
	if long := math.Max(fw, fh); long > float64(longEdge) {
		scale := float64(longEdge) / long
		fw, fh = fw*scale, fh*scale
	}
	patches := func(a, b float64) int { return int(math.Ceil(a/28) * math.Ceil(b/28)) }
	n := patches(fw, fh)
	if n > maxTokens {
		scale := math.Sqrt(float64(maxTokens) / float64(n))
		n = patches(fw*scale, fh*scale)
		if n > maxTokens {
			n = maxTokens
		}
	}
	return n
}

// OpenAI image costing (developers.openai.com images-vision guide, constants
// for the gpt-5 tier verified against /responses/input_tokens): tile models
// scale into 2048x2048 then shortest side to 768 and count 512px tiles at
// base + perTile; patch models count 32px patches capped at a budget, times a
// multiplier.
type oaiImageModel struct {
	base, perTile int     // tile models
	patchMult     float64 // patch models (base/perTile zero)
}

var oaiImagePrefixes = map[string]oaiImageModel{
	"gpt-4o-mini":  {base: 2833, perTile: 5667},
	"gpt-4.1-mini": {patchMult: 1.62},
	"gpt-4.1-nano": {patchMult: 2.46},
	"gpt-5-mini":   {patchMult: 1.62},
	"gpt-5-nano":   {patchMult: 2.46},
	"o4-mini":      {patchMult: 1.72},
	"o1":           {base: 75, perTile: 150},
	"o3":           {base: 75, perTile: 150},
	"gpt-5":        {base: 69, perTile: 140}, // measured; covers gpt-5.x
	"gpt-4o":       {base: 85, perTile: 170},
	"gpt-4.1":      {base: 85, perTile: 170},
	"gpt-4":        {base: 85, perTile: 170},
}

// OpenAIImage estimates image tokens for an OpenAI model at detail "high"
// (also the practical result of "auto" for typical images). detailLow uses
// the flat low-detail cost. Unknown dimensions assume 1024x1024.
func OpenAIImage(model string, w, h int, detailLow bool) int {
	spec, bestLen := oaiImageModel{base: 85, perTile: 170}, 0
	for prefix, s := range oaiImagePrefixes {
		if len(prefix) > bestLen && strings.HasPrefix(model, prefix) {
			spec, bestLen = s, len(prefix)
		}
	}
	if w <= 0 || h <= 0 {
		w, h = 1024, 1024
	}

	if spec.patchMult > 0 {
		patches := int(math.Ceil(float64(w)/32) * math.Ceil(float64(h)/32))
		if patches > 1536 {
			patches = 1536
		}
		return int(float64(patches)*spec.patchMult + 0.5)
	}

	if detailLow {
		return spec.base
	}
	fw, fh := float64(w), float64(h)
	if long := math.Max(fw, fh); long > 2048 {
		scale := 2048 / long
		fw, fh = fw*scale, fh*scale
	}
	if short := math.Min(fw, fh); short > 768 {
		scale := 768 / short
		fw, fh = fw*scale, fh*scale
	}
	tiles := int(math.Ceil(fw/512) * math.Ceil(fh/512))
	return spec.base + spec.perTile*tiles
}

var pdfPageRe = regexp.MustCompile(`/Type\s*/Page[^s]`)

// PDFPages counts the pages of a raw PDF, minimum 1.
func PDFPages(data []byte) int {
	if n := len(pdfPageRe.FindAll(data, -1)); n > 0 {
		return n
	}
	return 1
}

// Per-page PDF token estimates: each page is billed as extracted text plus a
// rendered page image. Measured: 555–1580 tokens/page (Anthropic),
// 664–800/page (OpenAI input_tokens) on sample documents.
const (
	ClaudePDFPageTokens = 1500
	OpenAIPDFPageTokens = 750
)
