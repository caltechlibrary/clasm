package filemanager

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/caltechlibrary/clasm/internal/awsclient"
)

// fakeS3 is a minimal in-memory S3 double for filemanager's tests --
// real enough to exercise Delimiter=/ grouping (ListObjectsV2's actual
// behavior), GetObject, PutObject, DeleteObject, and HeadObject without
// a real bucket. Keyed by full object key -> content.
type fakeS3 struct {
	awsclient.S3API
	objects map[string][]byte

	deleteObjectCalls []string
	putObjectCalls    []string
}

func newFakeS3(keys ...string) *fakeS3 {
	f := &fakeS3{objects: make(map[string][]byte)}
	for _, k := range keys {
		f.objects[k] = []byte("content of " + k)
	}
	return f
}

func (f *fakeS3) ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	prefix := aws.ToString(params.Prefix)
	delim := aws.ToString(params.Delimiter)

	var keys []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	out := &s3.ListObjectsV2Output{}
	seenPrefixes := make(map[string]bool)
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		if delim != "" {
			if i := strings.Index(rest, delim); i >= 0 {
				cp := prefix + rest[:i+1]
				if !seenPrefixes[cp] {
					seenPrefixes[cp] = true
					out.CommonPrefixes = append(out.CommonPrefixes, types.CommonPrefix{Prefix: aws.String(cp)})
				}
				continue
			}
		}
		out.Contents = append(out.Contents, types.Object{
			Key:  aws.String(k),
			Size: aws.Int64(int64(len(f.objects[k]))),
		})
	}
	return out, nil
}

func (f *fakeS3) GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	body, ok := f.objects[aws.ToString(params.Key)]
	if !ok {
		return nil, &notFoundErr{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: aws.Int64(int64(len(body)))}, nil
}

func (f *fakeS3) PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	key := aws.ToString(params.Key)
	f.putObjectCalls = append(f.putObjectCalls, key)
	body, err := io.ReadAll(params.Body)
	if err != nil {
		return nil, err
	}
	f.objects[key] = body
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) DeleteObject(ctx context.Context, params *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	key := aws.ToString(params.Key)
	f.deleteObjectCalls = append(f.deleteObjectCalls, key)
	delete(f.objects, key)
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3) HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	key := aws.ToString(params.Key)
	body, ok := f.objects[key]
	if !ok {
		return nil, &notFoundErr{}
	}
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(int64(len(body)))}, nil
}

type notFoundErr struct{}

func (*notFoundErr) Error() string { return "NoSuchKey: key does not exist" }
