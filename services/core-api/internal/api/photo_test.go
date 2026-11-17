package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func photoServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := filepath.Join(t.TempDir(), "photo")
	s := &Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), InternalToken: token, UserID: "owner",
		Now: func() time.Time { return now }, PhotoDir: dir}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

func testImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	return img
}

func sendPhoto(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PUT", url+"/api/v1/profile/photo", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestProfilePhoto(t *testing.T) {
	ts, dir := photoServer(t)
	if res := do(t, "GET", ts.URL+"/api/v1/profile/photo", "", nil); res.StatusCode != 404 {
		t.Errorf("no photo yet: %d", res.StatusCode)
	}

	// A JPEG carrying EXIF with a GPS position: the stored copy has no metadata.
	var j bytes.Buffer
	jpeg.Encode(&j, testImage(1200, 800), nil)
	exif := append([]byte{0xFF, 0xE1, 0x00, 0x16}, []byte("Exif\x00\x00GPSLatitude12.97\x00")...)
	withExif := append(append([]byte{}, j.Bytes()[:2]...), append(exif, j.Bytes()[2:]...)...)
	if res := sendPhoto(t, ts.URL, withExif); res.StatusCode != 200 {
		t.Fatalf("upload: %d", res.StatusCode)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
	saved, _ := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if bytes.Contains(saved, []byte("Exif")) || bytes.Contains(saved, []byte("GPS")) {
		t.Error("metadata kept")
	}
	if info, _ := files[0].Info(); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if files[0].Name() == "owner.jpg" {
		t.Error("user ID used in the file name")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(saved))
	if err != nil || cfg.Width != 512 || cfg.Height != 341 {
		t.Errorf("size %dx%d err %v", cfg.Width, cfg.Height, err)
	}
	p := decodeBody[store.Profile](t, do(t, "GET", ts.URL+"/api/v1/profile", "", nil))
	if p.PhotoUpdatedAt == nil {
		t.Error("profile doesn't report the photo")
	}
	res := do(t, "GET", ts.URL+"/api/v1/profile/photo", "", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("get: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// PNG is accepted; small ones are kept at their size.
	var pg bytes.Buffer
	png.Encode(&pg, testImage(200, 300))
	if res := sendPhoto(t, ts.URL, pg.Bytes()); res.StatusCode != 200 {
		t.Errorf("png: %d", res.StatusCode)
	}

	// Refused: not an image, an SVG, too large in pixels or bytes.
	var huge bytes.Buffer
	png.Encode(&huge, image.NewGray(image.Rect(0, 0, 5000, 40)))
	for name, body := range map[string][]byte{
		"text":   []byte("hello, this is not a picture at all"),
		"svg":    []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"pixels": huge.Bytes(),
	} {
		if res := sendPhoto(t, ts.URL, body); res.StatusCode != 415 {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
	if res := sendPhoto(t, ts.URL, make([]byte, 6<<20)); res.StatusCode != 413 {
		t.Errorf("6 MB: %d", res.StatusCode)
	}

	if res := do(t, "DELETE", ts.URL+"/api/v1/profile/photo", "", nil); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if files, _ := os.ReadDir(dir); len(files) != 0 {
		t.Errorf("file left after delete: %v", files)
	}
	if p := decodeBody[store.Profile](t, do(t, "GET", ts.URL+"/api/v1/profile", "", nil)); p.PhotoUpdatedAt != nil {
		t.Error("photo still reported")
	}
}
