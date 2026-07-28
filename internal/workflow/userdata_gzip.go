package workflow

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
)

// gzipMagic is gzip's own two-byte format signature -- used to detect
// whether a decoded user-data payload was gzip-compressed (this
// project's own writes, see encodeUserData) or plain text (every
// resource created before this change, or by any tool other than
// clasm), so decodeUserData can handle both (DECISIONS.md, "Always
// gzip-compress user-data before base64-encoding it").
var gzipMagic = []byte{0x1f, 0x8b}

// maxUserDataBytes is AWS's own documented limit on raw (gzip-
// compressed, pre-base64) EC2 user-data -- previously only mentioned in
// a source comment, never enforced (DECISIONS.md, "User-data pre-flight
// size check").
const maxUserDataBytes = 16384

// encodeUserData gzip-compresses plainText at gzip.BestCompression,
// then base64-encodes the compressed bytes -- the form every
// EC2/launch-template UserData write site now sends, so a large
// cloud-init file stays under AWS's 16384-byte raw user-data limit
// (cloud-init itself auto-detects and transparently decompresses
// gzip'd user-data). Returns an error if the compressed size still
// exceeds maxUserDataBytes -- some cloud-init files are too large even
// at maximum compression (DECISIONS.md, "User-data pre-flight size
// check"), and AWS's own bare 400 gives the operator no actionable
// information, so this fails before ever attempting the AWS call.
func encodeUserData(plainText string) (string, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	_, _ = gz.Write([]byte(plainText))
	_ = gz.Close()
	if buf.Len() > maxUserDataBytes {
		return "", fmt.Errorf("cloud-init content compresses to %d bytes, %d over AWS's %d-byte user-data limit -- trim the file and try again", buf.Len(), buf.Len()-maxUserDataBytes, maxUserDataBytes)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// decodeUserData base64-decodes encoded, then gunzips it if it starts
// with gzip's magic bytes -- otherwise returns the decoded bytes as-is.
// This is what makes encodeUserData's switch to always-gzip backward
// compatible: every read site can decode both this project's own
// gzip'd writes and any pre-existing plain-text user-data without
// needing to know which one a given resource has.
func decodeUserData(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(raw) < len(gzipMagic) || !bytes.Equal(raw[:len(gzipMagic)], gzipMagic) {
		return string(raw), nil
	}

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("opening gzip user-data: %w", err)
	}
	defer gz.Close()
	decompressed, err := io.ReadAll(gz)
	if err != nil {
		return "", fmt.Errorf("reading gzip user-data: %w", err)
	}
	return string(decompressed), nil
}
