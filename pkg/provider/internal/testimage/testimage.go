// Package testimage builds synthetic scenes for live image-edit checks: a
// house on a meadow, an alpha mask that frees a circle of sky, and a markup
// overlay that circles the same spot.
package testimage

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

const (
	Size = 1024

	// SpotX, SpotY and SpotRadius describe the circle in the sky that both
	// the mask and the markup single out.
	SpotX      = 780
	SpotY      = 230
	SpotRadius = 140
)

// Scene is a house on a meadow under a plain sky.
func Scene() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, Size, Size))

	sky := color.NRGBA{R: 150, G: 200, B: 240, A: 255}
	grass := color.NRGBA{R: 90, G: 160, B: 70, A: 255}
	wall := color.NRGBA{R: 200, G: 200, B: 195, A: 255}
	roof := color.NRGBA{R: 160, G: 60, B: 50, A: 255}
	door := color.NRGBA{R: 90, G: 60, B: 40, A: 255}

	for y := range Size {
		for x := range Size {
			switch {
			case x >= 150 && x < 400 && y >= 520 && y < 700:
				img.SetNRGBA(x, y, wall)
			case x >= 130 && x < 420 && y >= 450 && y < 520:
				img.SetNRGBA(x, y, roof)
			case y >= 600:
				img.SetNRGBA(x, y, grass)
			default:
				img.SetNRGBA(x, y, sky)
			}

			if x >= 250 && x < 300 && y >= 620 && y < 700 {
				img.SetNRGBA(x, y, door)
			}
		}
	}

	return encode(img)
}

// Mask is opaque everywhere except the spot, which is fully transparent.
func Mask() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, Size, Size))

	for y := range Size {
		for x := range Size {
			if distance(x, y) <= SpotRadius {
				img.SetNRGBA(x, y, color.NRGBA{})
				continue
			}

			img.SetNRGBA(x, y, color.NRGBA{A: 255})
		}
	}

	return encode(img)
}

// Markup is transparent except for a red ring around the spot and a red
// arrow pointing at it from the lower left.
func Markup() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, Size, Size))

	red := color.NRGBA{R: 230, G: 20, B: 20, A: 255}

	for y := range Size {
		for x := range Size {
			d := distance(x, y)

			if math.Abs(d-SpotRadius) <= 6 {
				img.SetNRGBA(x, y, red)
			}

			// arrow shaft from (420, 560) towards the ring along y = -x + 980
			if x >= 420 && x <= 640 && math.Abs(float64(y)-(-float64(x)+980)) <= 5 {
				img.SetNRGBA(x, y, red)
			}

			// arrow head near (640, 340)
			if x >= 600 && x <= 650 && y >= 330 && y <= 400 && (math.Abs(float64(y-340)) <= 5 || math.Abs(float64(x-640)) <= 5) {
				img.SetNRGBA(x, y, red)
			}
		}
	}

	return encode(img)
}

func distance(x, y int) float64 {
	return math.Hypot(float64(x-SpotX), float64(y-SpotY))
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer

	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}

	return buf.Bytes()
}
