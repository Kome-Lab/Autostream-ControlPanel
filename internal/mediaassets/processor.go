package mediaassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

type Processor struct {
	storage *DiskStorage
}

func NewProcessor(storage *DiskStorage) *Processor {
	return &Processor{storage: storage}
}

func (p *Processor) ProcessUpload(filename, declaredContentType string, body io.Reader) (ProcessedImage, error) {
	if p == nil || p.storage == nil || body == nil {
		return ProcessedImage{}, ErrInvalidImage
	}
	processingDir := filepath.Join(p.storage.root, ".processing")
	if err := os.MkdirAll(processingDir, 0o750); err != nil {
		return ProcessedImage{}, err
	}
	tmp, err := os.CreateTemp(processingDir, "upload-*")
	if err != nil {
		return ProcessedImage{}, err
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	n, err := io.Copy(tmp, io.LimitReader(body, MaxUploadBytes+1))
	if err != nil {
		return ProcessedImage{}, ErrInvalidImage
	}
	if n > MaxUploadBytes {
		return ProcessedImage{}, ErrUploadTooLarge
	}
	if n == 0 {
		return ProcessedImage{}, ErrInvalidImage
	}
	if err := tmp.Sync(); err != nil {
		return ProcessedImage{}, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return ProcessedImage{}, err
	}
	raw, err := io.ReadAll(io.LimitReader(tmp, MaxUploadBytes+1))
	if err != nil || int64(len(raw)) != n {
		return ProcessedImage{}, ErrInvalidImage
	}

	format, contentType, err := validateContainer(filename, declaredContentType, raw)
	if err != nil {
		return ProcessedImage{}, err
	}
	config, err := decodeConfig(format, raw)
	if err != nil {
		return ProcessedImage{}, ErrInvalidImage
	}
	if err := validateDimensions(config.Width, config.Height); err != nil {
		return ProcessedImage{}, err
	}
	decoded, err := decodeImage(format, raw)
	if err != nil {
		return ProcessedImage{}, ErrInvalidImage
	}
	if format == "jpeg" {
		decoded = applyEXIFOrientation(decoded, jpegEXIFOrientation(raw))
	}
	bounds := decoded.Bounds()
	if err := validateDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return ProcessedImage{}, err
	}

	var normalized bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&normalized, decoded); err != nil {
		return ProcessedImage{}, ErrInvalidImage
	}
	storageKey, digest, size, err := p.storage.WriteContentAddressed(bytes.NewReader(normalized.Bytes()), "png")
	if err != nil {
		return ProcessedImage{}, err
	}
	processed := ProcessedImage{
		StorageKey: storageKey,
		SHA256:     digest,
		MediaType:  "image/png",
		ByteSize:   size,
		Width:      bounds.Dx(),
		Height:     bounds.Dy(),
		Opaque:     imageIsOpaque(decoded),
	}
	if contentType == "" {
		return ProcessedImage{}, ErrInvalidImage
	}
	if err := p.Verify(processed); err != nil {
		_ = p.storage.Remove(storageKey)
		return ProcessedImage{}, err
	}
	return processed, nil
}

func (p *Processor) CreateVariant(source ProcessedImage, width, height int, forceOpaque bool) (ProcessedImage, error) {
	if err := validateDimensions(width, height); err != nil {
		return ProcessedImage{}, err
	}
	if err := p.Verify(source); err != nil {
		return ProcessedImage{}, err
	}
	f, err := p.storage.Open(source.StorageKey)
	if err != nil {
		return ProcessedImage{}, ErrIntegrity
	}
	decoded, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		return ProcessedImage{}, ErrIntegrity
	}

	sourceBounds := decoded.Bounds()
	crop := centerCropBounds(sourceBounds, width, height)
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	if forceOpaque {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				destination.SetNRGBA(x, y, color.NRGBA{R: 7, G: 17, B: 29, A: 255})
			}
		}
	}
	xdraw.CatmullRom.Scale(destination, destination.Bounds(), decoded, crop, xdraw.Over, nil)

	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&encoded, destination); err != nil {
		return ProcessedImage{}, err
	}
	storageKey, digest, size, err := p.storage.WriteContentAddressed(bytes.NewReader(encoded.Bytes()), "png")
	if err != nil {
		return ProcessedImage{}, err
	}
	variant := ProcessedImage{
		StorageKey: storageKey,
		SHA256:     digest,
		MediaType:  "image/png",
		ByteSize:   size,
		Width:      width,
		Height:     height,
		Opaque:     imageIsOpaque(destination),
	}
	if forceOpaque && !variant.Opaque {
		_ = p.storage.Remove(storageKey)
		return ProcessedImage{}, ErrIntegrity
	}
	if err := p.Verify(variant); err != nil {
		_ = p.storage.Remove(storageKey)
		return ProcessedImage{}, err
	}
	return variant, nil
}

func (p *Processor) Verify(expected ProcessedImage) error {
	if p == nil || p.storage == nil || strings.TrimSpace(expected.StorageKey) == "" {
		return ErrIntegrity
	}
	f, err := p.storage.Open(expected.StorageKey)
	if err != nil {
		return ErrIntegrity
	}
	defer f.Close()
	h := sha256.New()
	limited := io.LimitReader(f, MaxUploadBytes+1)
	raw, err := io.ReadAll(io.TeeReader(limited, h))
	if err != nil || int64(len(raw)) > MaxUploadBytes || int64(len(raw)) != expected.ByteSize {
		return ErrIntegrity
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected.SHA256) {
		return ErrIntegrity
	}
	if expected.MediaType != "image/png" || !validPNGContainer(raw) {
		return ErrIntegrity
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != expected.Width || config.Height != expected.Height {
		return ErrIntegrity
	}
	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil || imageIsOpaque(decoded) != expected.Opaque {
		return ErrIntegrity
	}
	return nil
}

func validateContainer(filename, declaredContentType string, raw []byte) (string, string, error) {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))
	declared := ""
	if strings.TrimSpace(declaredContentType) != "" {
		parsed, _, err := mime.ParseMediaType(declaredContentType)
		if err != nil {
			return "", "", ErrContentTypeMismatch
		}
		declared = strings.ToLower(parsed)
	}
	switch {
	case validPNGContainer(raw):
		if ext != ".png" || (declared != "" && declared != "image/png") {
			return "", "", ErrContentTypeMismatch
		}
		return "png", "image/png", nil
	case validJPEGContainer(raw):
		if (ext != ".jpg" && ext != ".jpeg") || (declared != "" && declared != "image/jpeg") {
			return "", "", ErrContentTypeMismatch
		}
		return "jpeg", "image/jpeg", nil
	case isWebPContainer(raw):
		if !validStaticWebPContainer(raw) {
			return "", "", ErrAnimatedImage
		}
		if ext != ".webp" || (declared != "" && declared != "image/webp") {
			return "", "", ErrContentTypeMismatch
		}
		return "webp", "image/webp", nil
	default:
		return "", "", ErrUnsupportedImage
	}
}

func decodeConfig(format string, raw []byte) (image.Config, error) {
	switch format {
	case "png":
		return png.DecodeConfig(bytes.NewReader(raw))
	case "jpeg":
		return jpeg.DecodeConfig(bytes.NewReader(raw))
	case "webp":
		return webp.DecodeConfig(bytes.NewReader(raw))
	default:
		return image.Config{}, ErrUnsupportedImage
	}
}

func decodeImage(format string, raw []byte) (image.Image, error) {
	switch format {
	case "png":
		return png.Decode(bytes.NewReader(raw))
	case "jpeg":
		return jpeg.Decode(bytes.NewReader(raw))
	case "webp":
		return webp.Decode(bytes.NewReader(raw))
	default:
		return nil, ErrUnsupportedImage
	}
}

func validateDimensions(width, height int) error {
	if width < 1 || height < 1 || width > MaxImageSide || height > MaxImageSide || int64(width)*int64(height) > MaxDecodedPixels {
		return ErrImageDimensions
	}
	return nil
}

func validPNGContainer(raw []byte) bool {
	if len(raw) < 20 || !bytes.Equal(raw[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		return false
	}
	offset := 8
	for offset+12 <= len(raw) {
		length := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		if length < 0 || offset+12+length > len(raw) {
			return false
		}
		typ := string(raw[offset+4 : offset+8])
		offset += 12 + length
		if typ == "IEND" {
			return length == 0 && offset == len(raw)
		}
	}
	return false
}

func validJPEGContainer(raw []byte) bool {
	if len(raw) < 4 || raw[0] != 0xff || raw[1] != 0xd8 {
		return false
	}
	offset := 2
	for offset < len(raw) {
		if raw[offset] != 0xff {
			return false
		}
		for offset < len(raw) && raw[offset] == 0xff {
			offset++
		}
		if offset >= len(raw) {
			return false
		}
		marker := raw[offset]
		offset++
		switch {
		case marker == 0xd9:
			// The first EOI marker is authoritative. Bytes after it make the
			// upload a polyglot rather than a single JPEG container.
			return offset == len(raw)
		case marker == 0xd8 || marker == 0x00:
			return false
		case marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7):
			continue
		}
		if offset+2 > len(raw) {
			return false
		}
		segmentLength := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
		if segmentLength < 2 || offset+segmentLength > len(raw) {
			return false
		}
		offset += segmentLength
		if marker != 0xda {
			continue
		}
		// Entropy-coded scan data permits byte-stuffed FF00 and restart/TEM
		// markers. Any other marker returns control to the segment parser.
		for offset < len(raw) {
			if raw[offset] != 0xff {
				offset++
				continue
			}
			markerStart := offset
			offset++
			for offset < len(raw) && raw[offset] == 0xff {
				offset++
			}
			if offset >= len(raw) {
				return false
			}
			scanMarker := raw[offset]
			if scanMarker == 0x00 || scanMarker == 0x01 || (scanMarker >= 0xd0 && scanMarker <= 0xd7) {
				offset++
				continue
			}
			offset = markerStart
			break
		}
	}
	return false
}

func isWebPContainer(raw []byte) bool {
	return len(raw) >= 12 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP"
}

func validStaticWebPContainer(raw []byte) bool {
	if !isWebPContainer(raw) || int(binary.LittleEndian.Uint32(raw[4:8]))+8 != len(raw) {
		return false
	}
	offset := 12
	for offset+8 <= len(raw) {
		chunkType := string(raw[offset : offset+4])
		chunkLength := int(binary.LittleEndian.Uint32(raw[offset+4 : offset+8]))
		offset += 8
		if chunkLength < 0 || offset+chunkLength > len(raw) {
			return false
		}
		if chunkType == "ANIM" || chunkType == "ANMF" ||
			(chunkType == "VP8X" && chunkLength >= 1 && raw[offset]&0x02 != 0) {
			return false
		}
		offset += chunkLength
		if chunkLength%2 == 1 {
			offset++
		}
	}
	return offset == len(raw)
}

func centerCropBounds(bounds image.Rectangle, targetWidth, targetHeight int) image.Rectangle {
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	if int64(sourceWidth)*int64(targetHeight) > int64(sourceHeight)*int64(targetWidth) {
		cropWidth := sourceHeight * targetWidth / targetHeight
		left := bounds.Min.X + (sourceWidth-cropWidth)/2
		return image.Rect(left, bounds.Min.Y, left+cropWidth, bounds.Max.Y)
	}
	cropHeight := sourceWidth * targetHeight / targetWidth
	top := bounds.Min.Y + (sourceHeight-cropHeight)/2
	return image.Rect(bounds.Min.X, top, bounds.Max.X, top+cropHeight)
}

func imageIsOpaque(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha != 0xffff {
				return false
			}
		}
	}
	return true
}

func jpegEXIFOrientation(raw []byte) int {
	for offset := 2; offset+4 <= len(raw) && raw[offset] == 0xff; {
		marker := raw[offset+1]
		offset += 2
		if marker == 0xd9 || marker == 0xda {
			break
		}
		if offset+2 > len(raw) {
			break
		}
		length := int(binary.BigEndian.Uint16(raw[offset : offset+2]))
		if length < 2 || offset+length > len(raw) {
			break
		}
		segment := raw[offset+2 : offset+length]
		if marker == 0xe1 && len(segment) >= 14 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation := tiffOrientation(segment[6:]); orientation >= 1 && orientation <= 8 {
				return orientation
			}
		}
		offset += length
	}
	return 1
}

func tiffOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 0 || ifd+2 > len(tiff) {
		return 1
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	for i := 0; i < count; i++ {
		offset := ifd + 2 + i*12
		if offset+12 > len(tiff) {
			return 1
		}
		if order.Uint16(tiff[offset:offset+2]) == 0x0112 && order.Uint16(tiff[offset+2:offset+4]) == 3 && order.Uint32(tiff[offset+4:offset+8]) == 1 {
			return int(order.Uint16(tiff[offset+8 : offset+10]))
		}
	}
	return 1
}

func applyEXIFOrientation(src image.Image, orientation int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if orientation < 2 || orientation > 8 {
		return src
	}
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func safeProcessorErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUploadTooLarge):
		return "upload_too_large"
	case errors.Is(err, ErrImageDimensions):
		return "invalid_image_dimensions"
	case errors.Is(err, ErrContentTypeMismatch):
		return "content_type_mismatch"
	case errors.Is(err, ErrAnimatedImage):
		return "animated_image_unsupported"
	case errors.Is(err, ErrUnsupportedImage):
		return "unsupported_image"
	case errors.Is(err, ErrIntegrity):
		return "media_asset_integrity"
	default:
		return "image_processing_failed"
	}
}

func verifyAspectRatio(width, height, targetWidth, targetHeight int, tolerance float64) error {
	if err := validateDimensions(width, height); err != nil {
		return err
	}
	actual := float64(width) / float64(height)
	target := float64(targetWidth) / float64(targetHeight)
	if delta := actual/target - 1; delta < -tolerance || delta > tolerance {
		return fmt.Errorf("%w: aspect ratio", ErrImageDimensions)
	}
	return nil
}
