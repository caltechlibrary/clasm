package rdmfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

var _ S3Reader = (*fakeS3)(nil)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	w.Close()
	return b.Bytes()
}

func newFetcher(t *testing.T, f *fakeS3) *Fetcher {
	t.Helper()
	return &Fetcher{S3: f, CacheDir: t.TempDir()}
}

func md5hex(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }

// ---- dumps ----

func TestListDumps_NewestFirstOnlySQLFilesDirectlyUnderTheSourceAcrossPages(t *testing.T) {
	f := newFakeS3()
	for _, k := range []string{"src/a-2026-11-15.sql.gz", "src/a-2026-11-17.sql.gz", "src/a-2026-11-16.sql", "src/notes.txt", "src/opensearch-snapshots/x/index-1", "other/b.sql.gz"} {
		f.put(k, []byte("x"))
	}
	o := f.objects["src/a-2026-11-17.sql.gz"]
	o.when = o.when.AddDate(0, 0, 1)
	f.objects["src/a-2026-11-17.sql.gz"] = o
	got, err := newFetcher(t, f).ListDumps(context.Background(), "b", "src")
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, d := range got {
		keys = append(keys, d.Key)
	}
	if len(keys) != 3 || keys[0] != "src/a-2026-11-17.sql.gz" {
		t.Errorf("keys = %v", keys)
	}
	for _, k := range keys {
		if strings.Contains(k, "notes") || strings.Contains(k, "opensearch") || strings.HasPrefix(k, "other") {
			t.Errorf("unexpected %q", k)
		}
	}
}

func TestFetchDump_AGoodGzipIsVerifiedAndKeptPrivate(t *testing.T) {
	f := newFakeS3()
	data := gz(t, "-- Dumped by pg_dump\nSELECT 1;\n")
	f.put("src/d.sql.gz", data)
	fe := newFetcher(t, f)
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(r.Path)
	if !bytes.Equal(got, data) {
		t.Error("the local copy differs from the object")
	}
	if !r.GzipOK || !r.MD5Verified || r.MD5 != md5hex(data) || r.Size != int64(len(data)) {
		t.Errorf("report = %+v", r)
	}
	if info, _ := os.Stat(r.Path); info.Mode().Perm() != 0o600 {
		t.Errorf("a dump holds user data and must be private, mode %v", info.Mode().Perm())
	}
	if !strings.HasPrefix(r.Path, fe.CacheDir) {
		t.Errorf("path %q is outside the cache", r.Path)
	}
}

func TestFetchDump_ACorruptGzipIsRefusedAndLeavesNothing(t *testing.T) {
	f := newFakeS3()
	data := gz(t, strings.Repeat("INSERT INTO t VALUES (1);\n", 2000))
	f.put("src/d.sql.gz", data[:len(data)-12]) // truncated: its size and md5 are self-consistent
	fe := newFetcher(t, f)
	_, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(fe.CacheDir); len(entries) != 0 {
		t.Errorf("a failed fetch must leave nothing behind: %v", entries)
	}
}

func TestFetchDump_ASizeMismatchIsRefused(t *testing.T) {
	f := newFakeS3()
	f.put("src/d.sql", []byte("SELECT 1;\n"))
	o := f.objects["src/d.sql"]
	o.lenLies = 999
	f.objects["src/d.sql"] = o
	_, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/d.sql")
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "size") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchDump_AnMD5MismatchIsRefusedButAMultipartETagIsNotComparable(t *testing.T) {
	f := newFakeS3()
	f.put("src/bad.sql", []byte("SELECT 1;\n"))
	o := f.objects["src/bad.sql"]
	o.etag = `"00000000000000000000000000000000"`
	f.objects["src/bad.sql"] = o
	if _, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/bad.sql"); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "MD5") {
		t.Errorf("md5 mismatch: err = %v", err)
	}
	f.put("src/multi.sql", []byte("SELECT 2;\n"))
	o = f.objects["src/multi.sql"]
	o.etag = `"d41d8cd98f00b204e9800998ecf8427e-5"`
	f.objects["src/multi.sql"] = o
	r, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/multi.sql")
	if err != nil || r.MD5Verified || r.GzipOK {
		t.Errorf("multipart: r=%+v err=%v", r, err)
	}
	if r.MD5 != md5hex([]byte("SELECT 2;\n")) {
		t.Errorf("the local MD5 is still reported: %q", r.MD5)
	}
}

func TestFetchDump_ASecondFetchOfAVerifiedFileDownloadsNothing(t *testing.T) {
	f := newFakeS3()
	f.put("src/d.sql.gz", gz(t, "SELECT 1;\n"))
	fe := newFetcher(t, f)
	if _, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz"); err != nil {
		t.Fatal(err)
	}
	before := f.getCount()
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || !r.Reused || !r.GzipOK {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if f.getCount() != before {
		t.Errorf("gets went from %d to %d", before, f.getCount())
	}
}

func TestFetchDump_AStaleLocalCopyIsReplaced(t *testing.T) {
	f := newFakeS3()
	f.put("src/d.sql", []byte("new content\n"))
	fe := newFetcher(t, f)
	if err := os.WriteFile(filepath.Join(fe.CacheDir, "d.sql"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql")
	if err != nil || r.Reused {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if b, _ := os.ReadFile(r.Path); string(b) != "new content\n" {
		t.Errorf("content = %q", b)
	}
}

func TestFetchDump_AMissingObjectIsErrNotFoundNamingIt(t *testing.T) {
	_, err := newFetcher(t, newFakeS3()).FetchDump(context.Background(), "b", "src/none.sql.gz")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "src/none.sql.gz") {
		t.Errorf("err = %v", err)
	}
}

// ---- snapshots ----

func putSnapshot(f *fakeS3, source, name string, files map[string]string) {
	for rel, content := range files {
		f.put(source+"/opensearch-snapshots/"+name+"/"+rel, []byte(content))
	}
}

var goodRepo = map[string]string{
	"index-5":             "gen5",
	"index.latest":        "\x00\x00\x00\x05",
	"meta-abc.dat":        "meta",
	"snap-abc.dat":        "snap",
	"indices/aaa/0/d.dat": "data0",
	"indices/aaa/1/d.dat": "data1",
	"indices/bbb/0/d.dat": "data2",
}

func TestListSnapshots_NewestFirst(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-20261115-030000", goodRepo)
	putSnapshot(f, "src", "rdm-20261117-030000", goodRepo)
	putSnapshot(f, "src", "rdm-20261116-030000", goodRepo)
	got, err := newFetcher(t, f).ListSnapshots(context.Background(), "b", "src")
	if err != nil || len(got) != 3 || got[0] != "rdm-20261117-030000" || got[2] != "rdm-20261115-030000" {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestFetchSnapshot_DownloadsEveryObjectIntoTheRepositoryRoot(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	putSnapshot(f, "src", "rdm-2", map[string]string{"index-1": "other"}) // a neighbour must not be pulled in
	fe := newFetcher(t, f)
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Objects != len(goodRepo) || r.Name != "rdm-1" {
		t.Errorf("report = %+v", r)
	}
	var total int64
	for rel, want := range goodRepo {
		b, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(rel)))
		if err != nil || string(b) != want {
			t.Errorf("%s: %q %v", rel, b, err)
		}
		total += int64(len(want))
	}
	if r.Bytes != total {
		t.Errorf("bytes = %d, want %d", r.Bytes, total)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "index-1")); err == nil {
		t.Error("another snapshot's file leaked in")
	}
}

func TestFetchSnapshot_ACorruptObjectFailsTheWholeFetchNamingItAndLeavesNoRepository(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	key := "src/opensearch-snapshots/rdm-1/indices/aaa/1/d.dat"
	o := f.objects[key]
	o.lenLies = 99
	f.objects[key] = o
	fe := newFetcher(t, f)
	_, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "indices/aaa/1/d.dat") {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(fe.CacheDir); len(entries) != 0 {
		t.Errorf("nothing may remain after a failed fetch: %v", entries)
	}
}

func TestFetchSnapshot_ARepositoryWithoutIndicesIsRefused(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", map[string]string{"index-1": "x", "snap-a.dat": "y"})
	_, err := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "indices/") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchSnapshot_NoSuchSnapshotIsErrNotFound(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	_, err := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-9")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "rdm-9") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchSnapshot_AKeyThatEscapesTheDirectoryIsRefused(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	f.put("src/opensearch-snapshots/rdm-1/../../escape.dat", []byte("x"))
	fe := newFetcher(t, f)
	_, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err == nil {
		t.Fatal("a key with .. must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(fe.CacheDir), "escape.dat")); statErr == nil {
		t.Error("a file was written outside the cache")
	}
}

func TestFetchSnapshot_ASecondFetchOfACompleteCopyDownloadsNothing(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	fe := newFetcher(t, f)
	if _, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1"); err != nil {
		t.Fatal(err)
	}
	before := f.getCount()
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil || !r.Reused || f.getCount() != before {
		t.Errorf("r=%+v err=%v gets %d -> %d", r, err, before, f.getCount())
	}
}

func TestFetchSnapshot_ACopyThatLostAFileIsFetchedAgain(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	fe := newFetcher(t, f)
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(r.Dir, "snap-abc.dat")); err != nil {
		t.Fatal(err)
	}
	r2, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil || r2.Reused {
		t.Fatalf("r=%+v err=%v", r2, err)
	}
	if _, err := os.Stat(filepath.Join(r2.Dir, "snap-abc.dat")); err != nil {
		t.Errorf("the missing file was not restored: %v", err)
	}
}

// ---- packing ----

func TestPackSnapshot_TarsTheRepositoryRootAndReportsItsSHA256(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	fe := newFetcher(t, f)
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(t.TempDir(), "repo.tar")
	sum, err := PackSnapshot(r.Dir, tarPath)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(tarPath)
	h := sha256.Sum256(b)
	if sum != hex.EncodeToString(h[:]) {
		t.Errorf("reported %s, file is %s", sum, hex.EncodeToString(h[:]))
	}
	tr := tar.NewReader(bytes.NewReader(b))
	got := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			c, _ := io.ReadAll(tr)
			got[hdr.Name] = string(c)
		}
	}
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	for rel, want := range goodRepo {
		if got[rel] != want {
			t.Errorf("%s in tar = %q, want %q (names %v)", rel, got[rel], want, names)
		}
	}
	if len(got) != len(goodRepo) {
		t.Errorf("tar holds %v", names)
	}
}

func TestPackSnapshot_IsDeterministic(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	r, _ := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	a, err1 := PackSnapshot(r.Dir, filepath.Join(t.TempDir(), "a.tar"))
	b, err2 := PackSnapshot(r.Dir, filepath.Join(t.TempDir(), "b.tar"))
	if err1 != nil || err2 != nil || a != b {
		t.Errorf("a=%s b=%s %v %v", a, b, err1, err2)
	}
}

func TestPackSnapshot_RefusesADirectoryWithoutIndicesAndLeavesNoTar(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index-1"), []byte("x"), 0o600)
	tarPath := filepath.Join(t.TempDir(), "x.tar")
	if _, err := PackSnapshot(dir, tarPath); err == nil {
		t.Error("want an error")
	}
	if _, err := os.Stat(tarPath); err == nil {
		t.Error("no tar may be left")
	}
}

var _ = s3.ListObjectsV2Input{}

// ---- gaps the first mutation pass found ----

func TestListDumps_IgnoresASQLFileInANestedDirectory(t *testing.T) {
	f := newFakeS3()
	f.put("src/a.sql.gz", []byte("x"))
	f.put("src/old/b.sql.gz", []byte("x"))
	got, _ := newFetcher(t, f).ListDumps(context.Background(), "b", "src")
	if len(got) != 1 || got[0].Key != "src/a.sql.gz" {
		t.Errorf("got %+v", got)
	}
}

func TestFetchDump_AnETagIsOnlyAnMD5WhenItIsThirtyTwoHexCharactersWithNoDash(t *testing.T) {
	for _, etag := range []string{`"d41d8cd98f00b204e9800998ecf842-5"`, `"d41d8cd98f00b204e9800998ecf8"`} { // 32 chars with a dash; 28 without
		f := newFakeS3()
		f.put("src/x.sql", []byte("SELECT 3;\n"))
		o := f.objects["src/x.sql"]
		o.etag = etag
		f.objects["src/x.sql"] = o
		r, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/x.sql")
		if err != nil || r.MD5Verified {
			t.Errorf("etag %s: r=%+v err=%v (it is not comparable, so the download must succeed unverified)", etag, r, err)
		}
	}
}

func TestFetchDump_AnObjectThatChangedAfterTheHeadIsRefused(t *testing.T) {
	f := newFakeS3()
	f.put("src/x.sql", []byte("SELECT 3;\n"))
	o := f.objects["src/x.sql"]
	o.getLenLies = 4242
	f.objects["src/x.sql"] = o
	if _, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/x.sql"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v", err)
	}
}

func TestFetchDump_ACachedCopyWithTheRightSizeButOtherContentIsNotReused(t *testing.T) {
	f := newFakeS3()
	good := []byte("SELECT 1;\n")
	f.put("src/x.sql", good)
	fe := newFetcher(t, f)
	if err := os.WriteFile(filepath.Join(fe.CacheDir, "x.sql"), []byte("SELECT 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchDump(context.Background(), "b", "src/x.sql")
	if err != nil || r.Reused {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if b, _ := os.ReadFile(r.Path); !bytes.Equal(b, good) {
		t.Errorf("content = %q", b)
	}
}

func TestFetchDump_ACachedGzipThatIsItselfBrokenIsNotReused(t *testing.T) {
	f := newFakeS3()
	bad := []byte("this is not gzip data at all")
	f.put("src/x.sql.gz", bad)
	fe := newFetcher(t, f)
	if err := os.WriteFile(filepath.Join(fe.CacheDir, "x.sql.gz"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fe.FetchDump(context.Background(), "b", "src/x.sql.gz"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a broken gzip must never be handed on, even from the cache: %v", err)
	}
}

func TestFetchSnapshot_ANonCanonicalKeyIsRefused(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	f.put("src/opensearch-snapshots/rdm-1/sub/../index-9", []byte("x"))
	if _, err := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-1"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v", err)
	}
}

func TestFetchSnapshot_ACompleteCopyWithoutItsMarkerIsFetchedAgain(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	fe := newFetcher(t, f)
	if _, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fe.CacheDir, "rdm-1.fetched")); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil || r.Reused {
		t.Errorf("a directory nothing vouches for must not be trusted: r=%+v err=%v", r, err)
	}
}

func TestFetchSnapshot_AStaleHalfFinishedDirectoryDoesNotLeakIntoTheCopy(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	fe := newFetcher(t, f)
	stale := filepath.Join(fe.CacheDir, "rdm-1.part")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(stale, "junk.dat"), []byte("junk"), 0o644)
	r, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Dir, "junk.dat")); err == nil {
		t.Error("a file from an earlier failed attempt is in the repository")
	}
}

func TestFetchSnapshot_ADirectoryPlaceholderObjectIsSkipped(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	f.put("src/opensearch-snapshots/rdm-1/indices/", nil)
	r, err := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	if err != nil || r.Objects != len(goodRepo) {
		t.Errorf("r=%+v err=%v", r, err)
	}
}

func TestPackSnapshot_EntriesCarryAFixedTimeSoTheSameTreeAlwaysPacksTheSame(t *testing.T) {
	f := newFakeS3()
	putSnapshot(f, "src", "rdm-1", goodRepo)
	r, _ := newFetcher(t, f).FetchSnapshot(context.Background(), "b", "src", "rdm-1")
	p := filepath.Join(t.TempDir(), "a.tar")
	if _, err := PackSnapshot(r.Dir, p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if h.ModTime.Unix() != 0 {
			t.Errorf("%s has time %v", h.Name, h.ModTime)
		}
	}
}

func TestFetchDump_AStaleHeadThatDisagreesWithTheBodyIsRefused(t *testing.T) {
	f := newFakeS3()
	f.put("src/x.sql", []byte("SELECT 3;\n"))
	o := f.objects["src/x.sql"]
	o.headLenLies = 7
	f.objects["src/x.sql"] = o
	if _, err := newFetcher(t, f).FetchDump(context.Background(), "b", "src/x.sql"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("err = %v", err)
	}
}

func TestFetchSnapshot_DownloadsConcurrentlyUpToTheWorkerLimit(t *testing.T) {
	f := newFakeS3()
	f.delay = 25 * time.Millisecond
	putSnapshot(f, "src", "rdm-1", goodRepo) // 7 objects
	fe := newFetcher(t, f)
	fe.Workers = 3
	if _, err := fe.FetchSnapshot(context.Background(), "b", "src", "rdm-1"); err != nil {
		t.Fatal(err)
	}
	if f.maxIn < 2 || f.maxIn > 3 {
		t.Errorf("peak concurrent downloads = %d, want 2 or 3 with Workers=3", f.maxIn)
	}
}

// ---- a dump uploaded in parts has no MD5 to compare, but it must still be reused ----
// Found on the 2026-11-18 rehearsal: every large dump is a multipart upload, so the
// MD5-only reuse rule never matched and restore-sql downloaded a 1 GiB dump twice.

func multipartDump(t *testing.T, f *fakeS3, key string, data []byte) {
	t.Helper()
	f.put(key, data)
	o := f.objects[key]
	o.etag = `"d41d8cd98f00b204e9800998ecf8427e-5"`
	f.objects[key] = o
}

func TestFetchDump_AMultipartDumpAlreadyFetchedIsReusedWithoutDownloadingAgain(t *testing.T) {
	f := newFakeS3()
	multipartDump(t, f, "src/d.sql.gz", gz(t, "SELECT 1;\n"))
	fe := newFetcher(t, f)
	if _, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz"); err != nil {
		t.Fatal(err)
	}
	before := f.getCount()
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || !r.Reused || r.MD5Verified || !r.GzipOK {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if f.getCount() != before {
		t.Errorf("a verified copy must not be downloaded again: gets %d -> %d", before, f.getCount())
	}
	if r.MD5 == "" {
		t.Error("the local MD5 is still reported")
	}
}

func TestFetchDump_AMultipartCopyOlderThanTheObjectIsNotReused(t *testing.T) {
	f := newFakeS3()
	data := gz(t, "SELECT 1;\n")
	multipartDump(t, f, "src/d.sql.gz", data)
	fe := newFetcher(t, f)
	local := filepath.Join(fe.CacheDir, "d.sql.gz")
	if err := os.WriteFile(local, data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := f.objects["src/d.sql.gz"].when.Add(-48 * time.Hour)
	if err := os.Chtimes(local, old, old); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || r.Reused {
		t.Errorf("an object changed after the copy was made: r=%+v err=%v", r, err)
	}
}

func TestFetchDump_AMultipartCopyOfTheWrongSizeOrWithBrokenGzipIsNotReused(t *testing.T) {
	f := newFakeS3()
	good := gz(t, "SELECT 1;\n")
	multipartDump(t, f, "src/d.sql.gz", good)
	fe := newFetcher(t, f)
	local := filepath.Join(fe.CacheDir, "d.sql.gz")
	if err := os.WriteFile(local, good[:len(good)-3], 0o600); err != nil { // wrong size
		t.Fatal(err)
	}
	if r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz"); err != nil || r.Reused {
		t.Errorf("wrong size: r=%+v err=%v", r, err)
	}
	broken := make([]byte, len(good)) // right size, not gzip
	copy(broken, "this is not gzip")
	if err := os.WriteFile(local, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || r.Reused {
		t.Fatalf("broken gzip of the right size: r=%+v err=%v", r, err)
	}
	if b, _ := os.ReadFile(r.Path); !bytes.Equal(b, good) {
		t.Error("the broken copy must be replaced by the verified download")
	}
}

func TestFetchDump_AMultipartPlainDumpIsReusedOnSizeAndAge(t *testing.T) {
	f := newFakeS3()
	multipartDump(t, f, "src/d.sql", []byte("SELECT 1;\n"))
	fe := newFetcher(t, f)
	if _, err := fe.FetchDump(context.Background(), "b", "src/d.sql"); err != nil {
		t.Fatal(err)
	}
	before := f.getCount()
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql")
	if err != nil || !r.Reused || r.GzipOK || f.getCount() != before {
		t.Errorf("r=%+v err=%v gets %d -> %d", r, err, before, f.getCount())
	}
}

func TestFetchDump_ACachedCopyWithAnOldTimeButTheRightMD5IsStillReusedForASinglePartObject(t *testing.T) {
	f := newFakeS3()
	data := gz(t, "SELECT 1;\n")
	f.put("src/d.sql.gz", data) // single-part: the ETag is the real MD5
	fe := newFetcher(t, f)
	local := filepath.Join(fe.CacheDir, "d.sql.gz")
	if err := os.WriteFile(local, data, 0o600); err != nil {
		t.Fatal(err)
	}
	old := f.objects["src/d.sql.gz"].when.Add(-48 * time.Hour)
	if err := os.Chtimes(local, old, old); err != nil {
		t.Fatal(err)
	}
	before := f.getCount()
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || !r.Reused || f.getCount() != before {
		t.Errorf("an identical MD5 proves the content, so the age does not matter: r=%+v err=%v", r, err)
	}
}

func TestFetchDump_AMultipartCopyThatIsValidGzipButADifferentSizeIsNotReused(t *testing.T) {
	f := newFakeS3()
	multipartDump(t, f, "src/d.sql.gz", gz(t, "SELECT 1;\n"))
	fe := newFetcher(t, f)
	other := gz(t, strings.Repeat("INSERT INTO t VALUES (2);\n", 50)) // valid gzip, newer than the object, wrong size
	local := filepath.Join(fe.CacheDir, "d.sql.gz")
	if err := os.WriteFile(local, other, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := fe.FetchDump(context.Background(), "b", "src/d.sql.gz")
	if err != nil || r.Reused {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if b, _ := os.ReadFile(r.Path); bytes.Equal(b, other) {
		t.Error("the wrong-size copy must be replaced by what S3 holds")
	}
}
