package market

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	// Image format decoders — registered for image.DecodeConfig sniffing so an
	// upload claiming to be a photo is validated as a real JPEG/PNG/GIF.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/gin-gonic/gin"
)

// maxImageBytes caps a single uploaded photo. Mobile camera shots are a few MB;
// 12MB leaves headroom for high-res without inviting abuse.
const maxImageBytes = 12 << 20

// maxImagesPerProduct bounds how many photos one listing can carry.
const maxImagesPerProduct = 8

// imageStore writes product photos under <root>/<productID>/<rand>.<ext> and
// serves them back. Filenames are random so they can't be enumerated.
type imageStore struct {
	root string
}

func newImageStore(root string) (*imageStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("image dir is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &imageStore{root: root}, nil
}

// extFor maps a sniffed image format to a file extension.
func extFor(format string) (string, bool) {
	switch format {
	case "jpeg":
		return ".jpg", true
	case "png":
		return ".png", true
	case "gif":
		return ".gif", true
	}
	return "", false
}

// Save validates and writes one uploaded file, returning its stored filename
// (not a path — just the basename under the product dir).
func (s *imageStore) Save(productID int64, fh *multipart.FileHeader) (string, error) {
	if fh.Size > maxImageBytes {
		return "", fmt.Errorf("图片过大(%.1fMB),上限 12MB", float64(fh.Size)/(1<<20))
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Sniff the format from the header before trusting the upload.
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return "", errors.New("无法识别的图片格式(仅支持 JPG/PNG/GIF)")
	}
	ext, ok := extFor(format)
	if !ok {
		return "", errors.New("仅支持 JPG/PNG/GIF 图片")
	}
	if cfg.Width == 0 || cfg.Height == 0 {
		return "", errors.New("图片尺寸无效")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	dir := filepath.Join(s.root, strconv.FormatInt(productID, 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := randName() + ext
	dst, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, io.LimitReader(f, maxImageBytes)); err != nil {
		_ = os.Remove(dst.Name())
		return "", err
	}
	return name, nil
}

// Delete removes one stored file for a product. Missing files are ignored.
func (s *imageStore) Delete(productID int64, name string) {
	if !safeName(name) {
		return
	}
	_ = os.Remove(filepath.Join(s.root, strconv.FormatInt(productID, 10), name))
}

// serveImage streams a stored product image from disk. Path params are
// validated to prevent traversal.
func (m *Market) serveImage(c *gin.Context) {
	pid := c.Param("product_id")
	file := c.Param("file")
	if !numericID(pid) || !safeName(file) {
		c.Status(http.StatusBadRequest)
		return
	}
	full := filepath.Join(m.images.root, pid, file)
	// Defence in depth: ensure the resolved path stays under root.
	if rel, err := filepath.Rel(m.images.root, full); err != nil || strings.HasPrefix(rel, "..") {
		c.Status(http.StatusBadRequest)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.File(full)
}

func randName() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// safeName allows only a bare basename of the shape <hex>.<ext>, blocking any
// path separators / traversal.
func safeName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return false
	}
	return name == filepath.Base(name)
}

func numericID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
