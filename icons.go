package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// trayIconSize is the size of the tray icon in pixels. The tray library shows it as 16 by 16 points,
// so 32 pixels give a sharp icon on a Retina screen.
const trayIconSize = 32

// trayColors are the colors of the states of the tray icon.
var trayColors = map[trayColor]color.RGBA{
	trayGreen:  {R: 0x34, G: 0xC7, B: 0x59, A: 0xFF},
	trayYellow: {R: 0xFF, G: 0xCC, B: 0x00, A: 0xFF},
	trayRed:    {R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF},
	trayGrey:   {R: 0x8E, G: 0x8E, B: 0x93, A: 0xFF},
	trayAlert:  {R: 0xFF, G: 0x3B, B: 0x30, A: 0xFF},
}

// badgeColor is the color of the dot that shows a new version.
var badgeColor = color.RGBA{R: 0x0A, G: 0x84, B: 0xFF, A: 0xFF}

// buildTrayIcon returns a PNG image: a filled circle in the color of the state.
// The alert state also has a white "!", so that the user can see it without the color.
// With hasBadge, a blue dot in the upper right corner shows that a new version waits.
func buildTrayIcon(state trayColor, hasBadge bool) []byte {
	fill := trayColors[state]
	img := image.NewRGBA(image.Rect(0, 0, trayIconSize, trayIconSize))
	center := float64(trayIconSize) / 2
	radius := center - 3
	for y := 0; y < trayIconSize; y++ {
		for x := 0; x < trayIconSize; x++ {
			distance := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			// The edge pixels get a part of the color, so that the edge is smooth.
			coverage := math.Max(0, math.Min(1, radius-distance+0.5))
			if coverage == 0 {
				continue
			}
			pixel := fill
			if state == trayAlert && isExclamationPixel(x, y) {
				pixel = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
			}
			img.SetRGBA(x, y, scaleAlpha(pixel, coverage))
		}
	}
	if hasBadge {
		drawBadge(img)
	}
	var buffer bytes.Buffer
	_ = png.Encode(&buffer, img)
	return buffer.Bytes()
}

// drawBadge draws the dot of a new version. A clear ring separates the dot from the circle.
func drawBadge(img *image.RGBA) {
	const centerX, centerY, radius, ring = 25.0, 7.0, 5.0, 1.5
	for y := 0; y < trayIconSize; y++ {
		for x := 0; x < trayIconSize; x++ {
			distance := math.Hypot(float64(x)+0.5-centerX, float64(y)+0.5-centerY)
			if coverage := math.Min(1, radius-distance+0.5); coverage > 0 {
				img.SetRGBA(x, y, scaleAlpha(badgeColor, coverage))
			} else if distance < radius+ring {
				img.SetRGBA(x, y, color.RGBA{})
			}
		}
	}
}

// isExclamationPixel tells if the pixel is a part of the "!" in the middle of the icon.
func isExclamationPixel(x, y int) bool {
	middle := trayIconSize / 2
	isColumn := x >= middle-2 && x < middle+2
	return isColumn && ((y >= 8 && y < 19) || (y >= 21 && y < 25))
}

// scaleAlpha multiplies the color by the coverage. The image type uses colors with premultiplied alpha.
func scaleAlpha(pixel color.RGBA, coverage float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(pixel.R) * coverage),
		G: uint8(float64(pixel.G) * coverage),
		B: uint8(float64(pixel.B) * coverage),
		A: uint8(float64(pixel.A) * coverage),
	}
}
