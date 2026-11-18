package rdmfetch

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeObject is one stored object. etag, when set, overrides the real MD5 so a
// test can model a multipart upload or a corrupted listing.
type fakeObject struct {
	data        []byte
	etag        string
	when        time.Time
	lenLies     int64 // when non-zero, the ContentLength S3 reports instead of len(data)
	getLenLies  int64 // when non-zero, only GetObject reports this ContentLength (the object changed after the HEAD)
	headLenLies int64 // when non-zero, only HeadObject reports this ContentLength (a stale HEAD)
}

// fakeS3 is an in-memory S3 that can only list and get: it has no other method,
// so a fetcher that tried to write would not compile against it.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string]fakeObject
	pageSize int
	gets     int
	inflight int
	maxIn    int
	delay    time.Duration
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string]fakeObject{}, pageSize: 2} }

func (f *fakeS3) put(key string, data []byte) {
	sum := md5.Sum(data)
	f.objects[key] = fakeObject{data: data, etag: `"` + hex.EncodeToString(sum[:]) + `"`, when: time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)}
}

func (f *fakeS3) getCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.gets }

func (f *fakeS3) ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := aws.ToString(in.Prefix)
	delim := aws.ToString(in.Delimiter)
	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	type entry struct {
		key, common string
	}
	var entries []entry
	seen := map[string]bool{}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				cp := prefix + rest[:i+1]
				if !seen[cp] {
					seen[cp] = true
					entries = append(entries, entry{common: cp})
				}
				continue
			}
		}
		entries = append(entries, entry{key: k})
	}
	start := 0
	if in.ContinuationToken != nil {
		start, _ = strconv.Atoi(*in.ContinuationToken)
	}
	end := min(start+f.pageSize, len(entries))
	out := &s3.ListObjectsV2Output{}
	for _, e := range entries[start:end] {
		if e.common != "" {
			out.CommonPrefixes = append(out.CommonPrefixes, s3types.CommonPrefix{Prefix: aws.String(e.common)})
			continue
		}
		o := f.objects[e.key]
		out.Contents = append(out.Contents, s3types.Object{Key: aws.String(e.key), Size: aws.Int64(int64(len(o.data))), ETag: aws.String(o.etag), LastModified: aws.Time(o.when)})
	}
	if end < len(entries) {
		out.IsTruncated = aws.Bool(true)
		out.NextContinuationToken = aws.String(strconv.Itoa(end))
	}
	return out, nil
}

func (f *fakeS3) GetObject(ctx context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	f.gets++
	f.inflight++
	if f.inflight > f.maxIn {
		f.maxIn = f.inflight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	o, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, &s3types.NoSuchKey{}
	}
	n := int64(len(o.data))
	if o.lenLies != 0 {
		n = o.lenLies
	}
	if o.getLenLies != 0 {
		n = o.getLenLies
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(o.data)), ContentLength: aws.Int64(n), ETag: aws.String(o.etag), LastModified: aws.Time(o.when)}, nil
}

func (f *fakeS3) HeadObject(ctx context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	o, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, &s3types.NotFound{}
	}
	n := int64(len(o.data))
	if o.lenLies != 0 {
		n = o.lenLies
	}
	if o.headLenLies != 0 {
		n = o.headLenLies
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(n), ETag: aws.String(o.etag), LastModified: aws.Time(o.when)}, nil
}
