// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package maptiles builds an XYZ tile pyramid of a DayZ map's satellite
// imagery from the map's data PBO (the client's worlds_<map>_data.pbo, or a
// modded map's data.pbo). The dedicated server's copy only has stubs.
//
// Nothing about the layout is assumed: every satellite tile
// (layers/S_<x>_<y>_lco.paa) comes with a terrain material
// (layers/P_<x>-<y>_*.rvmat) whose UV transform says which world rectangle the
// image covers, how it is oriented and how much of it overlaps its
// neighbours. The grid, the world size and the origin are derived from those,
// so maps that number their tiles from a different corner still land in the
// right place. World coordinates are DayZ's: x east, z north, metres.
package maptiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/woozymasta/paa"
	"github.com/woozymasta/pbo"
	"github.com/woozymasta/rap"
	"github.com/woozymasta/rvmat"
)

// TileSize is the edge of an output tile in pixels.
const TileSize = 256

// Pad is the colour outside the imagery: the parts of edge tiles beyond the
// map. The client paints the area around the map the same colour.
const Pad = "#0e1210"

// Generator is bumped when a change makes old tile sets wrong or worse.
const Generator = 1

// ErrStripped is returned for the dedicated server's copy of a map's data PBO.
var ErrStripped = errors.New("this is the stripped dedicated-server copy of the map data; use the DayZ client's worlds_<map>_data.pbo")

// Meta is the metadata.json that sits next to a tile set. X0/Z0 is the
// south-west corner of the imagery in world metres. Tiles run west to east and
// north to south; at MaxZoom one pixel is 1/PxPerM metres, and every zoom
// below has half the resolution of the one above.
type Meta struct {
	Map       string    `json:"map"`
	Hash      string    `json:"hash"`
	Generator int       `json:"generator"`
	X0        float64   `json:"x0"`
	Z0        float64   `json:"z0"`
	Width     float64   `json:"width"`
	Height    float64   `json:"height"`
	PxPerM    float64   `json:"px_per_m"`
	MaxZoom   int       `json:"max_zoom"`
	TileSize  int       `json:"tile_size"`
	Ext       string    `json:"ext"`
	Pad       string    `json:"pad"`
	Built     time.Time `json:"built"`
}

// Options tune Build.
type Options struct {
	Quality  int                   // JPEG quality, default 85
	Workers  int                   // default: number of CPUs
	Progress func(done, total int) // called after every tile, from several goroutines
}

var (
	sRe   = regexp.MustCompile(`(?i)^layers[\\/]s_(\d+)_(\d+)_lco\.paa$`)
	pRe   = regexp.MustCompile(`(?i)^layers[\\/]p_(\d+)-(\d+)_.*\.rvmat$`)
	texRe = regexp.MustCompile(`(?i)[\\/]s_(\d+)_(\d+)_lco\.(?:paa|png|tga)$`)
)

type tile struct {
	path         string
	flipX, flipY bool // the image runs west or south instead of east or north
	xmin, zmax   float64
}

// layout is the mosaic of the source tiles: a grid of "cores" (each tile
// without its overlap) that fits together without gaps.
type layout struct {
	cols, rows               int
	grid                     [][]*tile // [row][col], row 0 is the northernmost
	tilePx, stepPx, marginPx int
	ppm                      float64
	x0, z0, w, h             float64
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

func transform(b []byte, k [2]int) (*rvmat.UVTransform, error) {
	if bytes.HasPrefix(b, []byte("\x00raP")) {
		var err error
		if b, err = rap.DecodeToText(b, rap.DecodeOptions{DisableFloatNormalization: true}, rap.RenderOptions{}); err != nil {
			return nil, err
		}
	}
	m, err := rvmat.Parse(b, nil)
	if err != nil {
		return nil, err
	}
	for _, st := range m.Stages {
		if sm := texRe.FindStringSubmatch(st.Texture.Raw); sm != nil && atoi(sm[1]) == k[0] && atoi(sm[2]) == k[1] {
			return rvmat.EffectiveUVTransform(m, st)
		}
	}
	return nil, fmt.Errorf("no stage uses the satellite tile")
}

// round rounds to 1/scale, which removes the float32 noise of the source.
func round(v, scale float64) float64 { return math.Round(v*scale)/scale + 0 }

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// scan reads the tiles and materials of a PBO and derives the layout.
func scan(r *pbo.Reader) (*layout, error) {
	type key = [2]int
	tiles, mats := map[key]string{}, map[key]string{}
	for _, e := range r.Entries() {
		if m := sRe.FindStringSubmatch(e.Path); m != nil {
			if e.DataSize < 1024 {
				return nil, ErrStripped
			}
			tiles[key{atoi(m[1]), atoi(m[2])}] = e.Path
		} else if m := pRe.FindStringSubmatch(e.Path); m != nil {
			if k := (key{atoi(m[1]), atoi(m[2])}); mats[k] == "" {
				mats[k] = e.Path
			}
		}
	}
	if len(tiles) == 0 {
		return nil, errors.New("no satellite tiles (layers/S_*_lco.paa) in this PBO")
	}
	var first string
	for _, p := range tiles {
		first = p
		break
	}
	b, err := r.ReadEntry(first)
	if err != nil {
		return nil, err
	}
	cfg, err := paa.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", first, err)
	}
	if cfg.Width != cfg.Height {
		return nil, fmt.Errorf("satellite tiles are %dx%d, expected square", cfg.Width, cfg.Height)
	}
	l := &layout{tilePx: cfg.Width}

	var all []*tile
	var aside float64
	for k, p := range tiles {
		mp := mats[k]
		if mp == "" {
			return nil, fmt.Errorf("tile %s has no terrain material (layers/P_%03d-%03d_*.rvmat)", p, k[0], k[1])
		}
		mb, err := r.ReadEntry(mp)
		if err != nil {
			return nil, err
		}
		ut, err := transform(mb, k)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", mp, err)
		}
		if len(ut.Aside) < 2 || len(ut.Up) < 2 || len(ut.Dir) < 2 || len(ut.Pos) < 2 {
			return nil, fmt.Errorf("%s: short UV transform", mp)
		}
		a, d := ut.Aside[0], ut.Dir[1]
		if a == 0 || ut.Aside[1] != 0 || ut.Dir[0] != 0 || ut.Up[0] != 0 || ut.Up[1] != 0 || !near(math.Abs(a), math.Abs(d), 1e-6) {
			return nil, fmt.Errorf("%s: rotated or skewed tiles are not supported (%v)", mp, ut)
		}
		if aside == 0 {
			aside = math.Abs(a)
		} else if !near(aside, math.Abs(a), 1e-7) {
			return nil, fmt.Errorf("%s: tiles have different scales", mp)
		}
		// u = a*x + pos0 and v = d*z + pos1, with u and v running over 0..1.
		xu0, xu1 := -ut.Pos[0]/a, (1-ut.Pos[0])/a
		zv0, zv1 := -ut.Pos[1]/d, (1-ut.Pos[1])/d
		all = append(all, &tile{path: p, flipX: a < 0, flipY: d > 0, xmin: math.Min(xu0, xu1), zmax: math.Max(zv0, zv1)})
	}
	l.ppm = float64(l.tilePx) * aside

	xs, zs := distinct(all, func(t *tile) float64 { return t.xmin }), distinct(all, func(t *tile) float64 { return -t.zmax })
	if len(xs) < 2 || len(zs) < 2 || len(xs)*len(zs) != len(all) {
		return nil, fmt.Errorf("the %d satellite tiles do not form a complete %dx%d grid", len(all), len(xs), len(zs))
	}
	step := xs[1] - xs[0]
	for i := 1; i < len(xs); i++ {
		if !near(xs[i]-xs[i-1], step, 0.05) {
			return nil, errors.New("satellite tiles are not evenly spaced")
		}
	}
	for i := 1; i < len(zs); i++ {
		if !near(zs[i]-zs[i-1], step, 0.05) {
			return nil, errors.New("satellite tile rows are not evenly spaced, or differ from the columns")
		}
	}
	l.stepPx = int(math.Round(step * l.ppm))
	l.marginPx = (l.tilePx - l.stepPx) / 2
	if !near(float64(l.stepPx), step*l.ppm, 0.01) || l.marginPx < 0 || l.stepPx+2*l.marginPx != l.tilePx {
		return nil, fmt.Errorf("unexpected tile overlap: %d px tiles, %.3f m step at %.4f px/m", l.tilePx, step, l.ppm)
	}
	margin := float64(l.marginPx) / l.ppm
	l.cols, l.rows = len(xs), len(zs)
	l.w, l.h = float64(l.cols)*step, float64(l.rows)*step
	l.x0 = xs[0] + margin
	l.z0 = -zs[0] - margin - l.h
	l.grid = make([][]*tile, l.rows)
	for i := range l.grid {
		l.grid[i] = make([]*tile, l.cols)
	}
	for _, t := range all {
		c := sort.SearchFloat64s(xs, t.xmin-0.01)
		r := sort.SearchFloat64s(zs, -t.zmax-0.01)
		l.grid[r][c] = t
	}
	return l, nil
}

// distinct returns the sorted values of f, with values less than 1 cm apart merged.
func distinct(ts []*tile, f func(*tile) float64) []float64 {
	v := make([]float64, 0, len(ts))
	for _, t := range ts {
		v = append(v, f(t))
	}
	sort.Float64s(v)
	out := v[:1]
	for _, x := range v[1:] {
		if x-out[len(out)-1] > 0.01 {
			out = append(out, x)
		}
	}
	return out
}

func toRGBA(img image.Image) *image.RGBA {
	if p, ok := img.(*image.RGBA); ok {
		return p
	}
	out := image.NewRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// parallel runs f(0..n-1) on workers goroutines and returns the first error.
func parallel(ctx context.Context, n, workers int, f func(i int) error) error {
	var wg sync.WaitGroup
	var once sync.Once
	var first error
	idx := make(chan int)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if ctx.Err() != nil {
					continue
				}
				if err := f(i); err != nil {
					once.Do(func() { first = err })
				}
			}
		}()
	}
	for i := 0; i < n; i++ {
		idx <- i
	}
	close(idx)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}

// loadRow decodes one row of source tiles and stitches their cores into one
// north-up image of cols*stepPx by stepPx pixels.
func (l *layout) loadRow(ctx context.Context, r *pbo.Reader, mu *sync.Mutex, row, workers int) (*image.RGBA, error) {
	out := image.NewRGBA(image.Rect(0, 0, l.cols*l.stepPx, l.stepPx))
	err := parallel(ctx, l.cols, workers, func(c int) error {
		t := l.grid[row][c]
		mu.Lock()
		b, err := r.ReadEntry(t.path)
		mu.Unlock()
		if err != nil {
			return err
		}
		img, err := paa.Decode(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("%s: %w", t.path, err)
		}
		if img.Bounds().Dx() != l.tilePx || img.Bounds().Dy() != l.tilePx {
			return fmt.Errorf("%s: %v, expected %d px", t.path, img.Bounds().Size(), l.tilePx)
		}
		src := toRGBA(img)
		for y := 0; y < l.stepPx; y++ {
			sy := l.marginPx + y
			if t.flipY {
				sy = l.tilePx - 1 - sy
			}
			srow := src.Pix[sy*src.Stride : sy*src.Stride+l.tilePx*4]
			drow := out.Pix[y*out.Stride+c*l.stepPx*4 : y*out.Stride+(c+1)*l.stepPx*4]
			if !t.flipX {
				copy(drow, srow[l.marginPx*4:(l.marginPx+l.stepPx)*4])
				continue
			}
			for x := 0; x < l.stepPx; x++ {
				sx := l.tilePx - 1 - (l.marginPx + x)
				copy(drow[x*4:x*4+4], srow[sx*4:sx*4+4])
			}
		}
		return nil
	})
	return out, err
}

func tilePath(dir string, z, x, y int) string {
	return filepath.Join(dir, strconv.Itoa(z), strconv.Itoa(x), strconv.Itoa(y)+".jpg")
}

func writeTile(dir string, z, x, y int, img image.Image, q int) error {
	p := tilePath(dir, z, x, y)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	f, err := os.Create(p) //nolint:gosec // p is below the build directory, from integers
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: q}); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// padded fills img with the pad colour.
func padded(img *image.RGBA) {
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{0x0e, 0x12, 0x10, 255}}, image.Point{}, draw.Src)
}

// Build writes the pyramid for the PBO into dir (which it creates) and
// returns the metadata. It does not publish anything; see Publish.
func Build(ctx context.Context, pboPath, dir string, o Options) (*Meta, error) {
	if o.Quality == 0 {
		o.Quality = 85
	}
	if o.Workers == 0 {
		o.Workers = runtime.NumCPU()
	}
	r, err := pbo.Open(pboPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	l, err := scan(r)
	if err != nil {
		return nil, err
	}

	px, py := l.cols*l.stepPx, l.rows*l.stepPx
	ntx, nty := (px+TileSize-1)/TileSize, (py+TileSize-1)/TileSize
	maxZoom := 0
	for n := max(ntx, nty); n > 1; n = (n + 1) / 2 {
		maxZoom++
	}
	total := 0
	for nx, ny := ntx, nty; ; nx, ny = (nx+1)/2, (ny+1)/2 {
		total += nx * ny
		if nx == 1 && ny == 1 {
			break
		}
	}
	var done int
	var dmu sync.Mutex
	tick := func() {
		dmu.Lock()
		done++
		d := done
		dmu.Unlock()
		if o.Progress != nil {
			o.Progress(d, total)
		}
	}

	// The deepest level comes from the source tiles, one band of output
	// tiles at a time, with at most two source rows in memory.
	var mu sync.Mutex
	rows := map[int]*image.RGBA{}
	for ty := 0; ty < nty; ty++ {
		y0, y1 := ty*TileSize, min((ty+1)*TileSize, py)
		r0, r1 := y0/l.stepPx, (y1-1)/l.stepPx
		for k := range rows {
			if k < r0 {
				delete(rows, k)
			}
		}
		for k := r0; k <= r1; k++ {
			if rows[k] == nil {
				if rows[k], err = l.loadRow(ctx, r, &mu, k, o.Workers); err != nil {
					return nil, err
				}
			}
		}
		err = parallel(ctx, ntx, o.Workers, func(tx int) error {
			out := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
			padded(out)
			x0, x1 := tx*TileSize, min((tx+1)*TileSize, px)
			for y := y0; y < y1; y++ {
				sr := rows[y/l.stepPx]
				sy := y % l.stepPx
				copy(out.Pix[(y-y0)*out.Stride:], sr.Pix[sy*sr.Stride+x0*4:sy*sr.Stride+x1*4])
			}
			if err := writeTile(dir, maxZoom, tx, ty, out, o.Quality); err != nil {
				return err
			}
			tick()
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Every level above is the 2x2 average of the one below.
	for z, nx, ny := maxZoom-1, (ntx+1)/2, (nty+1)/2; z >= 0; z, nx, ny = z-1, (nx+1)/2, (ny+1)/2 {
		err = parallel(ctx, nx*ny, o.Workers, func(i int) error {
			x, y := i%nx, i/nx
			out := image.NewRGBA(image.Rect(0, 0, TileSize, TileSize))
			padded(out)
			for q := 0; q < 4; q++ {
				cx, cy := 2*x+q%2, 2*y+q/2
				f, err := os.Open(tilePath(dir, z+1, cx, cy))
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				img, err := jpeg.Decode(f)
				_ = f.Close()
				if err != nil {
					return err
				}
				half(out, toRGBA(img), (q%2)*TileSize/2, (q/2)*TileSize/2)
			}
			if err := writeTile(dir, z, x, y, out, o.Quality); err != nil {
				return err
			}
			tick()
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	m := &Meta{
		Generator: Generator, X0: round(l.x0, 1e3), Z0: round(l.z0, 1e3), Width: round(l.w, 1e3), Height: round(l.h, 1e3), PxPerM: round(l.ppm, 1e4),
		MaxZoom: maxZoom, TileSize: TileSize, Ext: "jpg", Pad: Pad, Built: time.Now().UTC().Truncate(time.Second),
	}
	return m, nil
}

// half draws src, shrunk to half size by averaging 2x2 pixels, into dst at (ox, oy).
func half(dst, src *image.RGBA, ox, oy int) {
	n := TileSize / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var s [3]int
			for d := 0; d < 4; d++ {
				i := (2*y+d/2)*src.Stride + (2*x+d%2)*4
				s[0], s[1], s[2] = s[0]+int(src.Pix[i]), s[1]+int(src.Pix[i+1]), s[2]+int(src.Pix[i+2])
			}
			j := (oy+y)*dst.Stride + (ox+x)*4
			//nolint:gosec // an average of four bytes fits a byte
			dst.Pix[j], dst.Pix[j+1], dst.Pix[j+2], dst.Pix[j+3] = uint8((s[0]+2)/4), uint8((s[1]+2)/4), uint8((s[2]+2)/4), 255
		}
	}
}

// SourceHash identifies a source PBO by its content.
func SourceHash(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the operator's own source file
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// Publish builds the tile set of a map's PBO below root/<name>/<hash> and
// makes it the current one (root/<name>/current). An unchanged source is not
// built again unless force is set. Older tile sets are kept for rollback.
func Publish(ctx context.Context, root, name, pboPath string, force bool, o Options) (*Meta, bool, error) {
	hash, err := SourceHash(pboPath)
	if err != nil {
		return nil, false, err
	}
	base := filepath.Join(root, name)
	final := filepath.Join(base, hash)
	if !force {
		if m, err := readMeta(final); err == nil && m.Generator == Generator {
			return m, false, point(base, hash)
		}
	}
	tmp := final + ".building"
	_ = os.RemoveAll(tmp)
	m, err := Build(ctx, pboPath, tmp, o)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, false, err
	}
	m.Map, m.Hash = name, hash
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, "metadata.json"), append(b, '\n'), 0o600); err != nil {
		return nil, false, err
	}
	if err := os.RemoveAll(final); err != nil {
		return nil, false, err
	}
	if err := os.Rename(tmp, final); err != nil {
		return nil, false, err
	}
	return m, true, point(base, hash)
}

// point switches root/<name>/current to the tile set of hash, atomically.
func point(base, hash string) error {
	l := filepath.Join(base, "current.tmp")
	_ = os.Remove(l)
	if err := os.Symlink(hash, l); err != nil {
		return err
	}
	return os.Rename(l, filepath.Join(base, "current"))
}

func readMeta(dir string) (*Meta, error) {
	b, err := os.ReadFile(filepath.Join(dir, "metadata.json")) //nolint:gosec // dir is built by this package
	if err != nil {
		return nil, err
	}
	var m Meta
	return &m, json.Unmarshal(b, &m)
}

// Current returns the metadata of the tile set a map is served from.
func Current(root, name string) (*Meta, error) { return readMeta(filepath.Join(root, name, "current")) }
