package mediaassets

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessorDefaultStorageRootMatchesInstallerStateDirectory(t *testing.T) {
	t.Setenv("AUTOSTREAM_MEDIA_ASSET_DIR", "")
	if runtime.GOOS == "windows" {
		programData := t.TempDir()
		t.Setenv("ProgramData", programData)
		if got, want := DefaultStorageRoot(), filepath.Join(programData, "AutoStream", "media-assets"); got != want {
			t.Fatalf("default storage root=%q want=%q", got, want)
		}
		return
	}
	if got, want := DefaultStorageRoot(), "/var/lib/autostream/control-panel/media-assets"; got != want {
		t.Fatalf("default storage root=%q want=%q", got, want)
	}
}

func TestProcessorAcceptsPNGJPEGAndStaticWebPByDecodingAndReencoding(t *testing.T) {
	processor, storage := newTestProcessor(t)
	pngBytes := encodePNG(t, 3, 2, false)
	jpegBytes := encodeJPEG(t, 3, 2)
	webpBytes, err := base64.StdEncoding.DecodeString(staticWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name, filename, mediaType string
		body                      []byte
	}{{"png", "safe.png", "image/png", pngBytes}, {"jpeg", "safe.jpg", "image/jpeg", jpegBytes}, {"webp", "safe.webp", "image/webp", webpBytes}} {
		t.Run(testCase.name, func(t *testing.T) {
			processed, err := processor.ProcessUpload(testCase.filename, testCase.mediaType, bytes.NewReader(testCase.body))
			if err != nil {
				t.Fatalf("ProcessUpload: %v", err)
			}
			if processed.MediaType != "image/png" || processed.StorageKey == "" || !strings.HasSuffix(processed.StorageKey, ".png") {
				t.Fatalf("normalized metadata=%#v", processed)
			}
			f, err := storage.Open(processed.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := png.Decode(f); err != nil {
				t.Fatalf("stored bytes are not re-encoded PNG: %v", err)
			}
			if err := processor.Verify(processed); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestProcessorRejectsUnsupportedAnimatedMismatchPolyglotAndLimits(t *testing.T) {
	processor, _ := newTestProcessor(t)
	pngBytes := encodePNG(t, 2, 2, false)
	var gifBytes bytes.Buffer
	if err := gif.Encode(&gifBytes, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black}), nil); err != nil {
		t.Fatal(err)
	}
	animated := animatedWebPHeader()
	jpegPolyglot := append(append([]byte{}, encodeJPEG(t, 2, 2)...), []byte("MZ-executable")...)
	jpegPolyglot = append(jpegPolyglot, 0xff, 0xd9)
	tests := []struct {
		name, file, media string
		body              []byte
		want              error
	}{{"svg", "x.svg", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), ErrUnsupportedImage}, {"gif", "x.gif", "image/gif", gifBytes.Bytes(), ErrUnsupportedImage}, {"animated webp", "x.webp", "image/webp", animated, ErrAnimatedImage}, {"extension mismatch", "x.jpg", "image/jpeg", pngBytes, ErrContentTypeMismatch}, {"mime mismatch", "x.png", "image/jpeg", pngBytes, ErrContentTypeMismatch}, {"PNG polyglot", "x.png", "image/png", append(append([]byte{}, pngBytes...), []byte("MZ-executable")...), ErrUnsupportedImage}, {"JPEG polyglot", "x.jpg", "image/jpeg", jpegPolyglot, ErrUnsupportedImage}, {"oversized", "x.png", "image/png", bytes.Repeat([]byte{'x'}, int(MaxUploadBytes)+1), ErrUploadTooLarge}}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := processor.ProcessUpload(testCase.file, testCase.media, bytes.NewReader(testCase.body))
			if !errors.Is(err, testCase.want) {
				t.Fatalf("error=%v want %v", err, testCase.want)
			}
		})
	}
	for _, dims := range [][2]int{{8193, 1}, {1, 8193}, {8000, 5001}, {0, 10}} {
		if err := validateDimensions(dims[0], dims[1]); !errors.Is(err, ErrImageDimensions) {
			t.Fatalf("dimensions %v error=%v", dims, err)
		}
	}
	pixelBomb := append([]byte(nil), pngBytes...)
	binary.BigEndian.PutUint32(pixelBomb[16:20], 8000)
	binary.BigEndian.PutUint32(pixelBomb[20:24], 5001)
	binary.BigEndian.PutUint32(pixelBomb[29:33], crc32.ChecksumIEEE(pixelBomb[12:29]))
	if _, err := processor.ProcessUpload("bomb.png", "image/png", bytes.NewReader(pixelBomb)); !errors.Is(err, ErrImageDimensions) {
		t.Fatalf("decoded pixel bomb error=%v", err)
	}
}

func TestProcessorAppliesEXIFOrientationAndStripsMetadata(t *testing.T) {
	processor, storage := newTestProcessor(t)
	raw := insertOrientationEXIF(t, encodeJPEG(t, 2, 3), 6)
	processed, err := processor.ProcessUpload("camera.jpeg", "image/jpeg", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if processed.Width != 3 || processed.Height != 2 {
		t.Fatalf("oriented dimensions=%dx%d", processed.Width, processed.Height)
	}
	stored, err := os.ReadFile(filepath.Join(storage.root, filepath.FromSlash(processed.StorageKey)))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("Exif")) {
		t.Fatal("EXIF marker survived normalization")
	}
	if !validPNGContainer(stored) {
		t.Fatal("normalized bytes are not strict PNG")
	}
}

func TestVariantIsRealCenterCropOpaqueAndTamperFails(t *testing.T) {
	processor, storage := newTestProcessor(t)
	pattern := image.NewNRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			pixel := color.NRGBA{R: 0, G: 255, B: 0, A: 255}
			if x < 2 {
				pixel = color.NRGBA{R: 255, A: 255}
			} else if x >= 6 {
				pixel = color.NRGBA{B: 255, A: 255}
			}
			pattern.SetNRGBA(x, y, pixel)
		}
	}
	pattern.SetNRGBA(2, 0, color.NRGBA{})
	var patternBytes bytes.Buffer
	if err := png.Encode(&patternBytes, pattern); err != nil {
		t.Fatal(err)
	}
	source, err := processor.ProcessUpload("../../operator-input.png", "image/png", bytes.NewReader(patternBytes.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(source.StorageKey, "operator") || strings.Contains(source.StorageKey, "..") {
		t.Fatalf("source filename reached storage key: %q", source.StorageKey)
	}
	variant, err := processor.CreateVariant(source, 4, 4, true)
	if err != nil {
		t.Fatal(err)
	}
	if variant.Width != 4 || variant.Height != 4 || !variant.Opaque || variant.SHA256 == source.SHA256 {
		t.Fatalf("variant=%#v", variant)
	}
	variantReader, err := storage.Open(variant.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	variantImage, err := png.Decode(variantReader)
	_ = variantReader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(variantImage.At(0, 0)).(color.NRGBA); got != (color.NRGBA{R: 7, G: 17, B: 29, A: 255}) {
		t.Fatalf("transparent center-crop pixel was not composited on the approved background: %#v", got)
	}
	for _, point := range []image.Point{{1, 0}, {0, 3}, {3, 3}} {
		if got := color.NRGBAModel.Convert(variantImage.At(point.X, point.Y)).(color.NRGBA); got != (color.NRGBA{G: 255, A: 255}) {
			t.Fatalf("variant stretched edge bands instead of center cropping at %v: %#v", point, got)
		}
	}
	wrongHash := variant
	wrongHash.SHA256 = strings.Repeat("0", 64)
	if err = processor.Verify(wrongHash); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("forged hash Verify=%v", err)
	}
	wrongDimensions := variant
	wrongDimensions.Width++
	if err = processor.Verify(wrongDimensions); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("forged dimensions Verify=%v", err)
	}
	wrongType := variant
	wrongType.MediaType = "image/jpeg"
	if err = processor.Verify(wrongType); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("forged media type Verify=%v", err)
	}
	path, err := storage.resolve(variant.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteAt([]byte("tamper"), 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err = processor.Verify(variant); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered Verify=%v", err)
	}
}

func TestMemoryRepositoryDraftClaimAuthorizationReferenceAndGC(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return base }
	session, err := repo.CreateUploadSession(context.Background(), "user-a", base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := repo.Upload(context.Background(), UploadInput{SessionID: session.ID, UserID: "user-a", UsageType: "scene_background", Filename: "x.png", ContentType: "image/png", Body: bytes.NewReader(encodePNG(t, 4, 4, false))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.EnsureVariant(context.Background(), "user-b", asset.ID, 2, 2, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user variant error=%v", err)
	}
	variant, err := repo.EnsureVariant(context.Background(), "user-a", asset.ID, 2, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := repo.EnsureVariant(context.Background(), "user-a", asset.ID, 2, 2, false)
	if err != nil || reused.ID != variant.ID {
		t.Fatalf("same variant was not reused: first=%s second=%s err=%v", variant.ID, reused.ID, err)
	}
	if err = repo.ClaimDraft(context.Background(), "user-b", session.ID, "stream-1", base); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-user claim=%v", err)
	}
	if err = repo.ClaimDraft(context.Background(), "user-a", session.ID, "stream-1", base); err != nil {
		t.Fatal(err)
	}
	if err = repo.ClaimDraft(context.Background(), "user-a", session.ID, "stream-1", base); err != nil {
		t.Fatalf("same claim not idempotent: %v", err)
	}
	repo.ReferenceVariant("stream-1", variant.ID)
	internal, err := repo.OpenInternalVariant(context.Background(), "stream-1", variant.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = internal.Reader.Close()
	if _, err = repo.OpenInternalVariant(context.Background(), "stream-2", variant.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-stream fetch=%v", err)
	}
	if err = repo.SoftDeleteAsset(context.Background(), "user-a", asset.ID, base); err != nil {
		t.Fatal(err)
	}
	removed, err := repo.GarbageCollect(context.Background(), base.Add(25*time.Hour), 10)
	if err != nil || removed != 0 {
		t.Fatalf("referenced GC removed=%d err=%v", removed, err)
	}
	repo.mu.Lock()
	_, claimedSessionRetained := repo.sessions[session.ID]
	repo.mu.Unlock()
	if claimedSessionRetained {
		t.Fatal("expired claimed upload session was not cleaned")
	}
	repo.mu.Lock()
	delete(repo.references, "stream-1")
	repo.mu.Unlock()
	removed, err = repo.GarbageCollect(context.Background(), base.Add(25*time.Hour), 10)
	if err != nil || removed != 1 {
		t.Fatalf("unreferenced GC removed=%d err=%v", removed, err)
	}
}

func TestMemoryGarbageCollectionPreservesSharedContentAddressedBlobs(t *testing.T) {
	repo, err := NewMemoryRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	repo.now = func() time.Time { return base }
	body := encodePNG(t, 6, 4, false)
	upload := func(userID, streamID string) (Asset, Variant) {
		session, uploadErr := repo.CreateUploadSession(context.Background(), userID, base.Add(time.Hour))
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		asset, uploadErr := repo.Upload(context.Background(), UploadInput{SessionID: session.ID, UserID: userID, UsageType: "scene_background", Filename: "same.png", ContentType: "image/png", Body: bytes.NewReader(body)})
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		variant, uploadErr := repo.EnsureVariant(context.Background(), userID, asset.ID, 3, 2, false)
		if uploadErr != nil {
			t.Fatal(uploadErr)
		}
		if uploadErr = repo.ClaimDraft(context.Background(), userID, session.ID, streamID, base); uploadErr != nil {
			t.Fatal(uploadErr)
		}
		return asset, variant
	}
	firstAsset, firstVariant := upload("user-a", "stream-a")
	secondAsset, secondVariant := upload("user-b", "stream-b")
	if firstAsset.StorageKey != secondAsset.StorageKey || firstVariant.StorageKey != secondVariant.StorageKey {
		t.Fatal("identical content did not share content-addressed storage")
	}
	if err = repo.SoftDeleteAsset(context.Background(), "user-a", firstAsset.ID, base); err != nil {
		t.Fatal(err)
	}
	if removed, gcErr := repo.GarbageCollect(context.Background(), base.Add(25*time.Hour), 10); gcErr != nil || removed != 1 {
		t.Fatalf("shared-blob GC removed=%d err=%v", removed, gcErr)
	}
	if err = repo.processor.Verify(processedFromAsset(secondAsset)); err != nil {
		t.Fatalf("GC removed the surviving asset blob: %v", err)
	}
	if err = repo.VerifyVariant(context.Background(), secondAsset.ID, secondVariant.ID); err != nil {
		t.Fatalf("GC removed the surviving variant blob: %v", err)
	}
}

func newTestProcessor(t *testing.T) (*Processor, *DiskStorage) {
	t.Helper()
	storage, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewProcessor(storage), storage
}
func encodePNG(t *testing.T, width, height int, transparent bool) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			alpha := uint8(255)
			if transparent && x == 0 && y == 0 {
				alpha = 0
			}
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(20 + x), G: uint8(40 + y), B: 80, A: alpha})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(40 + x), G: uint8(80 + y), B: 120, A: 255})
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func insertOrientationEXIF(t *testing.T, jpegBytes []byte, orientation uint16) []byte {
	t.Helper()
	tiff := make([]byte, 26)
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	app := []byte{0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2)}
	app = append(app, segment...)
	return append(append(append([]byte{}, jpegBytes[:2]...), app...), jpegBytes[2:]...)
}
func animatedWebPHeader() []byte {
	chunk := append([]byte("VP8X"), []byte{10, 0, 0, 0, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0}...)
	size := 4 + len(chunk)
	raw := append([]byte("RIFF"), byte(size), byte(size>>8), byte(size>>16), byte(size>>24))
	raw = append(raw, []byte("WEBP")...)
	return append(raw, chunk...)
}

const staticWebPBase64 = "UklGRooJAABXRUJQVlA4IH4JAAAyLwCdASqWAGQAPo04lUeioYwGg0EUBGJZQDWZNsO1JP5n64fB3Ub8v+o9XG3d53fTuYGQ1jfacgmJk6PthcDb6Dzr0z01vyivwkv9E+4YUHpwH65k6mVaIXqrC4XTBQLqkGc1rCgGes04gKPUimJAmZhyXeG4DBy84mCBiQxZwB3y0ABGURmcpLymWUnJPKhGe8RujkoIZ810gU5JiH/ONuOi9qprpe/D+igi50G8+UMtjxOVM02DI1cX8zoiV/kuK0uHAtfQaqJC3eZhgnSAYCLcn9Ayj9pMHbfk0JJRbdF9H8CZCBhGkcIF2K+oGIR+gVW/6aRvmPtQNcIoyzgdFht+Z31lpq0fF/hD2JlqnmNkvDsKndGC/A+Z2BH0/lTXb9S/ELRNKqWKIrfN/h2Pao6wijZj9KO5+yNBvsnvdZyNmFFB2O/JHVnn3XtYdzUEN9w2Fz52sfwOAMXrMIYRcnd3nEYDHjHyw4dvT5hhr5BZSlFAHgz7I3AWw5qCIy/YgAD+/p/aKcNszx8Xd4P/UesGZn9KuPC+bM7Ftm6MOeuMu5ZP3zWAOb4skNx3FFcXmMGazmsEGYQxHgVSeNZ9FqVet4IUc/QajL5J2TiLIyAJvSnMT8RZXKotqUhB00DSZ+hzvShMFGwcuPBTvvABW6TRYILdCUNwFVOGZl4yyQ9vGi5HuKDPa0gsLt5fP8S4wD8dJy27Tx3P+MUlS3egiSAY3YnlR2aEQlh866iFPPH4U06y6+VGtvbq+N2Kh7z14i/+zoWLhk9pvfWkhXMMm9MPLtl0gOEtc9xLInLyeKbb3kbUD60/vRcE6d6302jpc9f8tyCW9qcg2LhBeY7H2akeaH/6bXaX5rc45emsA+58yiAwxbaGeWFF5DyCqUMWbwX+BIHTlFjTeatv8Sl8hjklsykxPbFpSt0dCM//nn9ZdFg3tLTgq6vbkKhe7dWNNSGsafAD2DYlAyGH4P0320LlribfnfkovkKWxuky/vTuGOBnAr/wZcL2eyg+34CczxKpZKh77B+Y/VUqH4LnC2BOEdIPmgkE/zOGa2S5wyNqfdctPTNhYMOtR/HFOukVkv/v5q5E4alohykAaUISEiLqRbaZkkTkhypdOAfifqYybCSqlCYCwsaqVL4sDhbep/dRfVog8tmgSoeh+MIl+D/ePR8eD56tz6EYEKnHVN8AaXvpFK50F3SFMLcnaEMJhUJc8PvGF/Ez/q7X+HKQ3J84TzFJbscxr7eozHeFMMH9/h0l5DXY4aTv+iIR4gM0dnbjbEHgsKP61ALeNz28bvmwhHa0rFBa9uJVskb1W3k5cAMzC1RkxfJ/sT++R+7jjNsNc3XkIKUrwP6nZrwzhpXAvJx4tFQoPhnq1k754EgDJG16UBY37s0uMFIAsT+slJHQ0OGmnE8aWr9oXIe02bm/CAIXyEWvxnHcHTTxIqhFD4XzHGJ+MCe7opIY/dEkOPcKUD6zNQ8IPOT/XpDvWGw/ok/UcILHoyQ3QdCgUqFDr1rm1Jt5wWbZcWSNhXjf1LuKzwJrkf1rHf/Ktf4tAtfWr/rQhun31sTg2rbi5fWKOZul29eY3ngUYVPHgSb/vvPisPqqXdfnJLpilz0u/VvCPFVofn1VADfJKNOJkXtCIxQ/v7RKZMrtdUkmRJZswSHIZ9950jHQi7KTbIozDSPMHUy5aw2VzfNS47v6d4YOvGY2pMW5Q4B6vh1E+FirurI+iYkoyBWyfAJxKHc58PQ5CYOUXqC7pdinZViLBy70kYbbPbgU16bokAP2zAR/UBW9+GTlfdAUNdGskU7nGzUbge7rt45UqaTZcKTu5GbN3YflPxU2uS7NxWGOuCgtGCu1O2zX12MVXSKgHlYv6pddY6EWs6QeEpvgtKAdQv8MJmISVUS1y3+Xom1owgeGRyqpgO9dulNmPt3UdK4PBiVz97n4QbX6QRooaAlPvFmUgYNO1CJXiWGkzP0vNP2JNu7Uymtxhz7UZCAHjW2aFJ7s6cNOZIdKmCkq2bw5iA4jZSPs+snTiT8gFyUKnSK7Lvc7rUOJNq9qvNoZtAgC9mMDhOA4WVjNR2cBJKQgzfgtRsVLMpbbb3FJroDkux5VhZ2nB16TBoEqgYqQVo5WdivKjNOLGJqAfcX2hMjE3UK1Yu8DT5qfQrRuAK2Y5rGut0SWSmpLZX5oCO+KXgbeh9mzk1ULYJcaWCVCKBy5bGrsnEUN/9L8qkahJ2sF4FJp/aG7WATm/9nNfvkAi8lhzP/kWYQa9HUJpUL3gBJcTRd4yFXMPA3Sd0p4NQBULx1DdGWxeCP9eyvD39+nWkHgYJIfrEqlpXaD5TrpJt5O/XbQa7IlyHjPimjnSQsjf5WXdfIGOL21QHGApYH6u54q1jf6WGac8ynys7oJFEM36NyUp+UTsX4pxn1RaWGRKJ8EUk3XJ5GXVGQO9BB7YID2vR537253t2WNYirDbju9w8bfBJ02jDOgEjZRSMP4VZLutXotpzQo8259eLACChjWX8ODdcvEf/OwTwQNoI8hZGimdkNEgILhM0zYlOe/fvv4VdkxvU3oMXhie9v16T7kN9sL2GPzB//P0FLhM+QKOxbdguTnMDvH6VbEoeuX6wO/o2wec6id/QOZb0e5hbVrq2UgcXpbHm0bYn+DuA5e4FT50zUUlVe7cTvmnstNZ+p0Xx903zN/WMjmLvgt1/b2syHzhRgTFraP9Do3ny9hNZnIrCrGjzHRtrVqe+tXgR6ZNZ+m/jwyFG7P2rXbjnkhpHuEMADn/iT25su+3c8AO61vNksZfCsbt4+kycfRJ+i4XiIP08ro2g7+yyYpRvnPmyJ9ojMVMcGtxMwqA0xX3VFsDzaRi/RhJi2Oq3eNKX0RmNIU44qhLkJGgqfqTFv0iHdegTV1gC1kmG5RjbTZPcOHR1p1IeEJIHrfBdSa+gUASMi1S7t3crCjBtIcmPizFbEGi/EzDV7APO+vG523/eSG2foiiRUUpHFULlY4/yxOcoewd28fFY+vXN5LbUbZKIV5H8GiUlX6VfPimYmh0cF/plxhM9cKZjyoroVYgx2CS0YDaRx1cXFEkQfP5XjpzRKf4p907sdYKFcqEPQlbmHFjEidsSKEpR+BW97QhyY5R5qhJLhYeW9z8daTdGUKGE35tq2v7YKorbyGClrbuz8sZhepmUhbcbem+wvwkD1AW074/fSpNdTCIzlajUOz++SHPqSubek2AAAAAAA="
