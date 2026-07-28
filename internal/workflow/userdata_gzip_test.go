package workflow

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestEncodeUserData_RoundTripsThroughDecodeUserData(t *testing.T) {
	plain := "#cloud-config\npackages:\n  - sshfs\n"
	encoded, err := encodeUserData(plain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := decodeUserData(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}

func TestEncodeUserData_ProducesGzipContent(t *testing.T) {
	encoded, err := encodeUserData("#cloud-config\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("unexpected error decoding base64: %v", err)
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Errorf("expected gzip magic bytes, got first bytes %v", raw[:min(2, len(raw))])
	}
}

func TestEncodeUserData_ShrinksLargeText(t *testing.T) {
	// A real-world case: a large, repetitive cloud-init YAML should
	// compress well under AWS's 16384-byte user-data limit.
	large := strings.Repeat("  - some-package-name-in-a-long-list\n", 1000)
	encoded, err := encodeUserData(large)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(encoded) >= len(large) {
		t.Errorf("expected compressed+base64 output to be smaller than the original %d bytes, got %d", len(large), len(encoded))
	}
}

func TestEncodeUserData_UsesBestCompression(t *testing.T) {
	// Irregular, word-salad-like text is what distinguishes gzip's
	// default compression level (6) from BestCompression (9) -- a
	// perfectly periodic repeat (like TestEncodeUserData_ShrinksLargeText's
	// fixture) compresses identically at both levels, since DEFLATE's
	// match-finding already saturates on a single repeated pattern. This
	// is the same real-world gap that left granian-rdm-v14's cloud-init
	// file 287 bytes over the limit at level 6 and only 192 over at
	// level 9 (DECISIONS.md, "User-data pre-flight size check").
	large := wordSaladText(57849) // same raw byte count as the incident's real cloud-init file

	var level6 bytes.Buffer
	gz6, err := gzip.NewWriterLevel(&level6, gzip.DefaultCompression)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := gz6.Write([]byte(large)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := gz6.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var best bytes.Buffer
	gz9, err := gzip.NewWriterLevel(&best, gzip.BestCompression)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := gz9.Write([]byte(large)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := gz9.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if best.Len() >= level6.Len() {
		t.Fatalf("test fixture doesn't distinguish compression levels: level6=%d bestCompression=%d -- pick different filler text", level6.Len(), best.Len())
	}

	encoded, err := encodeUserData(large)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("unexpected error decoding base64: %v", err)
	}
	if len(raw) != best.Len() {
		t.Errorf("compressed size = %d, want %d (gzip.BestCompression's own measured size for this input)", len(raw), best.Len())
	}
}

// wordSaladText returns n bytes of deterministic (fixed-seed), irregular
// word-salad text -- varied enough that gzip's compression level (6 vs.
// BestCompression) actually produces different output sizes, unlike a
// single perfectly-periodic repeated string.
func wordSaladText(n int) string {
	words := []string{
		"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa",
		"lambda", "mu", "nu", "xi", "omicron", "pi", "rho", "sigma", "tau", "upsilon",
		"package-name-one", "package-name-two", "install-script", "runcmd", "write_files",
		"permissions", "owner", "content", "path", "source", "destination-directory",
	}
	r := rand.New(rand.NewSource(42))
	var b strings.Builder
	for b.Len() < n {
		for i, count := 0, 1+r.Intn(4); i < count; i++ {
			b.WriteString(words[r.Intn(len(words))])
			b.WriteString(" ")
		}
		b.WriteString("\n")
	}
	return b.String()[:n]
}

// pseudoRandomBytes returns n deterministic, near-incompressible bytes
// (xorshift32, fixed seed) -- gzip can't meaningfully compress this, so
// its compressed size tracks n almost 1:1 (a fixed gzip header/trailer
// overhead plus DEFLATE's per-block "stored" overhead), which is what
// makes the size-limit tests below able to reason about exact byte
// counts instead of guessing at real cloud-init content.
func pseudoRandomBytes(n int) []byte {
	b := make([]byte, n)
	state := uint32(0x2545f491)
	for i := range b {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		b[i] = byte(state)
	}
	return b
}

func gzipBestCompressionSize(t *testing.T, data []byte) int {
	t.Helper()
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := gz.Write(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return buf.Len()
}

func TestEncodeUserData_ErrorsWhenCompressedOverLimit(t *testing.T) {
	data := pseudoRandomBytes(20000)
	compressedSize := gzipBestCompressionSize(t, data)
	if compressedSize <= maxUserDataBytes {
		t.Fatalf("test fixture doesn't exceed the limit: compressed size %d, want > %d", compressedSize, maxUserDataBytes)
	}

	_, err := encodeUserData(string(data))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", compressedSize)) {
		t.Errorf("error %q does not mention the compressed size %d", err.Error(), compressedSize)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", maxUserDataBytes)) {
		t.Errorf("error %q does not mention the %d-byte limit", err.Error(), maxUserDataBytes)
	}
}

func TestEncodeUserData_SucceedsUnderLimit(t *testing.T) {
	data := pseudoRandomBytes(10000)
	compressedSize := gzipBestCompressionSize(t, data)
	if compressedSize > maxUserDataBytes {
		t.Fatalf("test fixture exceeds the limit: compressed size %d, want <= %d", compressedSize, maxUserDataBytes)
	}

	encoded, err := encodeUserData(string(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	decoded, err := decodeUserData(encoded)
	if err != nil {
		t.Fatalf("unexpected error decoding: %v", err)
	}
	if decoded != string(data) {
		t.Error("round-trip mismatch")
	}
}

func TestEncodeUserData_BoundaryAtTheLimit(t *testing.T) {
	// Binary-search the exact byte count at which pseudoRandomBytes'
	// compressed size crosses maxUserDataBytes, then confirm
	// encodeUserData succeeds exactly at that boundary and fails one
	// byte past it -- measured, not assumed.
	full := pseudoRandomBytes(20000)
	lo, hi := 0, len(full)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if gzipBestCompressionSize(t, full[:mid]) <= maxUserDataBytes {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	atLimit := full[:lo]
	if got := gzipBestCompressionSize(t, atLimit); got > maxUserDataBytes {
		t.Fatalf("boundary search produced a payload over the limit: %d > %d", got, maxUserDataBytes)
	}
	if _, err := encodeUserData(string(atLimit)); err != nil {
		t.Errorf("unexpected error right at the boundary: %v", err)
	}

	overLimit := full[:lo+1]
	got := gzipBestCompressionSize(t, overLimit)
	if got <= maxUserDataBytes {
		t.Skip("one byte more didn't push the compressed size over the limit (gzip block-boundary effect) -- not a meaningful boundary case here")
	}
	if _, err := encodeUserData(string(overLimit)); err == nil {
		t.Errorf("expected an error one byte past the boundary (compressed size %d, limit %d)", got, maxUserDataBytes)
	}
}

func TestDecodeUserData_BackwardCompatibleWithPlainBase64(t *testing.T) {
	plain := "#cloud-config\npackages:\n  - sshfs\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	got, err := decodeUserData(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q (plain, non-gzip base64 must still decode correctly)", got, plain)
	}
}

func TestDecodeUserData_ErrorsOnMalformedBase64(t *testing.T) {
	if _, err := decodeUserData("not valid base64!!!"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecodeUserData_ErrorsOnCorruptGzipStream(t *testing.T) {
	// Valid gzip magic bytes, but truncated/corrupt gzip data after that.
	corrupt := base64.StdEncoding.EncodeToString([]byte{0x1f, 0x8b, 0x08, 0x00, 0x00})
	if _, err := decodeUserData(corrupt); err == nil {
		t.Fatal("expected an error")
	}
}

// helper mirroring the real gzip round-trip, used only to sanity-check
// the test fixtures above match what compress/gzip actually produces.
func gzipThenBase64(t *testing.T, plain string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(plain)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestDecodeUserData_DecodesGzipProducedElsewhere(t *testing.T) {
	plain := "#cloud-config\npackages:\n  - sshfs\n"
	encoded := gzipThenBase64(t, plain)
	got, err := decodeUserData(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}
