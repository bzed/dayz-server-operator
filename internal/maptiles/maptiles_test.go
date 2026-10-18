// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package maptiles

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/woozymasta/paa"
	"github.com/woozymasta/pbo"
)

// A synthetic map: 2x2 source tiles of 256 px with 8 px overlap on every
// side, painted from a colour function of the world position, so the output
// can be checked pixel by pixel whichever way the tiles are numbered.
const (
	tpx    = 256
	margin = 8
	step   = tpx - 2*margin
)

func truth(x, z, x0, z0, w, h float64) (r, g uint8) {
	return uint8((x - x0) / w * 255), uint8((z - z0) / h * 255)
}

func synth(t *testing.T, flipX, flipY bool, x0, z0 float64) string {
	t.Helper()
	return synthExt(t, flipX, flipY, x0, z0, "paa")
}

// synthExt is synth with the extension the materials name their texture by.
func synthExt(t *testing.T, flipX, flipY bool, x0, z0 float64, ext string) string {
	t.Helper()
	const cols, rows = 2, 2
	w, h := float64(cols*step), float64(rows*step)
	a := 1.0 / tpx
	var inputs []pbo.Input
	add := func(name string, data []byte) {
		inputs = append(inputs, pbo.Input{Path: name, ModTime: time.Unix(0, 0).UTC(),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }})
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			xs := x0 + float64(step*c) - margin
			zs := z0 + float64(step*(rows-1-r)) - margin
			ua, p0 := a, -a*xs
			if flipX {
				ua, p0 = -a, a*(xs+tpx)
			}
			vd, p1 := -a, a*(zs+tpx)
			if flipY {
				vd, p1 = a, -a*zs
			}
			img := image.NewRGBA(image.Rect(0, 0, tpx, tpx))
			for j := 0; j < tpx; j++ {
				for i := 0; i < tpx; i++ {
					x, z := xs+float64(i)+0.5, zs+tpx-float64(j)-0.5
					if flipX {
						x = xs + tpx - float64(i) - 0.5
					}
					if flipY {
						z = zs + float64(j) + 0.5
					}
					rr, gg := truth(x, z, x0, z0, w, h)
					img.Set(i, j, color.RGBA{rr, gg, 128, 255})
				}
			}
			var pb bytes.Buffer
			if err := paa.Encode(&pb, img); err != nil {
				t.Fatal(err)
			}
			add(fmt.Sprintf(`layers\S_%03d_%03d_lco.paa`, c, r), pb.Bytes())
			add(fmt.Sprintf(`layers\P_%03d-%03d_L01.rvmat`, c, r), []byte(fmt.Sprintf(
				"class Stage0 { texture=\"x\\layers\\s_%03d_%03d_lco.%s\"; texGen=3; };\n"+
					"class TexGen3 { uvSource=\"worldPos\"; class uvTransform { aside[]={%v,0,0}; up[]={0,0,%v}; dir[]={0,%v,0}; pos[]={%v,%v,0}; }; };\n",
				c, r, ext, ua, a, vd, p0, p1)))
		}
	}
	p := filepath.Join(t.TempDir(), "data.pbo")
	if _, err := pbo.PackFile(context.Background(), p, inputs, pbo.PackOptions{}); err != nil {
		t.Fatal(err)
	}
	return p
}

func pixel(t *testing.T, dir string, z, x, y, i, j int) (r, g uint8) {
	t.Helper()
	b, err := os.ReadFile(tilePath(dir, z, x, y))
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	c := color.RGBAModel.Convert(img.At(i, j)).(color.RGBA)
	return c.R, c.G
}

func close8(a, b uint8) bool { return int(a)-int(b) <= 12 && int(b)-int(a) <= 12 }

func TestBuildPlacesPixelsInAnyOrientation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		flipX, flipY bool
		x0, z0       float64
	}{
		{"north-west origin", false, false, 0, 0},
		{"mirrored east-west", true, false, 0, 0},
		{"image rows run south", false, true, 0, 0},
		{"both flipped, offset origin", true, true, 100, -128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := synth(t, tc.flipX, tc.flipY, tc.x0, tc.z0)
			dir := t.TempDir()
			m, err := Build(context.Background(), src, dir, Options{})
			if err != nil {
				t.Fatal(err)
			}
			w := float64(2 * step)
			if m.X0 != tc.x0 || m.Z0 != tc.z0 || m.Width != w || m.Height != w || m.PxPerM != 1 || m.MaxZoom != 1 {
				t.Fatalf("meta: %+v", m)
			}
			// world position -> pixel at the deepest zoom, the way a client computes it
			for _, p := range [][2]float64{{10, 10}, {230, 40}, {240, 240}, {470, 15}, {15, 470}, {470, 470}, {300, 200}} {
				x, z := tc.x0+p[0], tc.z0+p[1]
				px, py := int(p[0]), int(w-p[1])
				wr, wg := truth(x, z, tc.x0, tc.z0, w, w)
				r, g := pixel(t, dir, m.MaxZoom, px/TileSize, py/TileSize, px%TileSize, py%TileSize)
				if !close8(r, wr) || !close8(g, wg) {
					t.Errorf("world (%v,%v): got %d,%d want %d,%d", x, z, r, g, wr, wg)
				}
				// one zoom out: the same place at half resolution
				r, g = pixel(t, dir, 0, 0, 0, px/2, py/2)
				if !close8(r, wr) || !close8(g, wg) {
					t.Errorf("zoom 0 at world (%v,%v): got %d,%d want %d,%d", x, z, r, g, wr, wg)
				}
			}
		})
	}
}

// Some mods ship materials that still name the source image
// (ccs\legacy\...\s_010_000_lco.png) although the PBO holds the .paa.
func TestBuildAcceptsMaterialsNamingSourceImages(t *testing.T) {
	for _, ext := range []string{"png", "tga"} {
		if _, err := Build(context.Background(), synthExt(t, false, false, 0, 0, ext), t.TempDir(), Options{}); err != nil {
			t.Errorf("%s: %v", ext, err)
		}
	}
}

func TestBuildRejectsStrippedServerCopy(t *testing.T) {
	var inputs []pbo.Input
	inputs = append(inputs, pbo.Input{Path: `layers\S_000_000_lco.paa`, ModTime: time.Unix(0, 0).UTC(),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(make([]byte, 172))), nil }})
	p := filepath.Join(t.TempDir(), "stub.pbo")
	if _, err := pbo.PackFile(context.Background(), p, inputs, pbo.PackOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(context.Background(), p, t.TempDir(), Options{}); !errors.Is(err, ErrStripped) {
		t.Fatalf("want ErrStripped, got %v", err)
	}
}

func TestPublishIsIdempotentAndSwitchesCurrent(t *testing.T) {
	src := synth(t, false, false, 0, 0)
	root := t.TempDir()
	m, built, err := Publish(context.Background(), root, "testmap", src, false, Options{})
	if err != nil || !built || m.Map != "testmap" || m.Hash == "" {
		t.Fatalf("first publish: %+v %v %v", m, built, err)
	}
	if _, built, err = Publish(context.Background(), root, "testmap", src, false, Options{}); err != nil || built {
		t.Fatalf("an unchanged source must not be built again: built=%v err=%v", built, err)
	}
	if _, built, err = Publish(context.Background(), root, "testmap", src, true, Options{}); err != nil || !built {
		t.Fatalf("force must rebuild: built=%v err=%v", built, err)
	}
	cur, err := Current(root, "testmap")
	if err != nil || cur.Hash != m.Hash {
		t.Fatalf("current: %+v %v", cur, err)
	}
	if _, err := os.Stat(filepath.Join(root, "testmap", m.Hash, "0", "0", "0.jpg")); err != nil {
		t.Fatal(err)
	}
}
