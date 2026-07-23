package workflow

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncodeUserData_RoundTripsThroughDecodeUserData(t *testing.T) {
	plain := "#cloud-config\npackages:\n  - sshfs\n"
	encoded := encodeUserData(plain)
	got, err := decodeUserData(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != plain {
		t.Errorf("got %q, want %q", got, plain)
	}
}

func TestEncodeUserData_ProducesGzipContent(t *testing.T) {
	encoded := encodeUserData("#cloud-config\n")
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
	encoded := encodeUserData(large)
	if len(encoded) >= len(large) {
		t.Errorf("expected compressed+base64 output to be smaller than the original %d bytes, got %d", len(large), len(encoded))
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
