package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	_ "image/png" // decode PNG uploads
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// The profile photo. Uploads are decoded and re-encoded as JPEG, which drops all
// metadata (EXIF, including GPS location), and scaled down to 512 px. The file
// stays on this machine.

const (
	maxPhoto     = 5 << 20 // 5 MiB upload
	maxPhotoDim  = 4096    // refuse larger images before decoding them
	photoMaxSide = 512
)

func (s *Server) photoPath() string {
	sum := sha256.Sum256([]byte(s.UserID)) // never put the user ID itself in a path
	return filepath.Join(s.PhotoDir, hex.EncodeToString(sum[:8])+".jpg")
}

func (s *Server) putPhoto(w http.ResponseWriter, r *http.Request) {
	if s.PhotoDir == "" {
		writeErr(w, http.StatusServiceUnavailable, "photos are not configured")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPhoto))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the photo is too large (5 MB at most)")
		return
	}
	out, err := cleanPhoto(data)
	if err != nil {
		writeErr(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	if err := os.MkdirAll(s.PhotoDir, 0o700); err != nil {
		s.internalErr(w, r, err)
		return
	}
	// Write then rename, so a failed upload never leaves half a photo.
	tmp, err := os.CreateTemp(s.PhotoDir, "upload-*.tmp")
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	_, werr := tmp.Write(out)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), s.photoPath())
	}
	if werr != nil {
		os.Remove(tmp.Name())
		s.internalErr(w, r, werr)
		return
	}
	if err := s.Store.SetPhoto(r.Context(), s.UserID, s.Now()); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.getProfile(w, r)
}

// cleanPhoto checks the upload is a JPEG or PNG of sane size, and returns a
// metadata-free JPEG no larger than photoMaxSide on either side.
func cleanPhoto(data []byte) ([]byte, error) {
	errBad := errors.New("upload a JPEG or PNG photo")
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, errBad
	}
	if cfg.Width < 32 || cfg.Height < 32 || cfg.Width > maxPhotoDim || cfg.Height > maxPhotoDim {
		return nil, errors.New("the photo must be between 32 and 4096 pixels on each side")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errBad
	}
	img = shrink(img, photoMaxSide)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// shrink scales an image down (never up) so its longer side is at most max,
// averaging each block of source pixels.
func shrink(src image.Image, max int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return src
	}
	nw, nh := max, h*max/w
	if h > w {
		nw, nh = w*max/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+(y+1)*h/nh
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+(x+1)*w/nw
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			if n == 0 {
				continue
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), uint8(a/n>>8)
		}
	}
	return dst
}

func (s *Server) getPhoto(w http.ResponseWriter, r *http.Request) {
	at, err := s.Store.PhotoUpdatedAt(r.Context(), s.UserID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if at == nil || s.PhotoDir == "" {
		writeErr(w, http.StatusNotFound, "no photo")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, s.photoPath())
}

func (s *Server) deletePhoto(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeletePhoto(r.Context(), s.UserID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	if s.PhotoDir != "" {
		if err := os.Remove(s.photoPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.internalErr(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
