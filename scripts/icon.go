//go:build ignore

// Convert the source application PNG into deterministic Windows and macOS icons.
// This only resizes and packages the source image; it does not alter its design.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"

	"golang.org/x/image/draw"
)

func main() {
	if err := generateIcons(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generateIcons() error {
	file, err := os.Open("assets/icon.png")
	if err != nil {
		return err
	}
	icon, err := png.Decode(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	bounds := icon.Bounds()
	if bounds.Dx() != bounds.Dy() || bounds.Dx() < 256 {
		return fmt.Errorf("assets/icon.png must be a square PNG of at least 256 pixels; got %dx%d", bounds.Dx(), bounds.Dy())
	}
	images := map[int][]byte{}
	for _, size := range []int{16, 24, 32, 48, 64, 128, 256, 512, 1024} {
		resized := image.NewNRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(resized, resized.Bounds(), icon, bounds, draw.Src, nil)
		var data bytes.Buffer
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&data, resized); err != nil {
			return err
		}
		images[size] = data.Bytes()
	}
	if err := writeICO("assets/icon.ico", images); err != nil {
		return err
	}
	return writeICNS("assets/icon.icns", images)
}

func writeICO(path string, images map[int][]byte) error {
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var data bytes.Buffer
	for _, value := range []uint16{0, 1, uint16(len(sizes))} {
		if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
			return err
		}
	}
	offset := uint32(6 + 16*len(sizes))
	for _, size := range sizes {
		dimension := byte(size) // ICO uses zero to represent 256 pixels.
		data.Write([]byte{dimension, dimension, 0, 0})
		for _, value := range []uint16{1, 32} {
			if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
				return err
			}
		}
		for _, value := range []uint32{uint32(len(images[size])), offset} {
			if err := binary.Write(&data, binary.LittleEndian, value); err != nil {
				return err
			}
		}
		offset += uint32(len(images[size]))
	}
	for _, size := range sizes {
		data.Write(images[size])
	}
	return os.WriteFile(path, data.Bytes(), 0o644)
}

func writeICNS(path string, images map[int][]byte) error {
	// Modern PNG-based ICNS entries include 1x and Retina representations.
	types := []struct {
		name string
		size int
	}{
		{"icp4", 16}, {"icp5", 32}, {"icp6", 64},
		{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024},
		{"ic11", 32}, {"ic12", 64}, {"ic13", 256}, {"ic14", 512},
	}
	var contents bytes.Buffer
	for _, entry := range types {
		contents.WriteString(entry.name)
		if err := binary.Write(&contents, binary.BigEndian, uint32(8+len(images[entry.size]))); err != nil {
			return err
		}
		contents.Write(images[entry.size])
	}
	var data bytes.Buffer
	data.WriteString("icns")
	if err := binary.Write(&data, binary.BigEndian, uint32(contents.Len()+8)); err != nil {
		return err
	}
	data.Write(contents.Bytes())
	return os.WriteFile(path, data.Bytes(), 0o644)
}
