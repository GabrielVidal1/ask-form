package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

var pngHead = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)

type upRig struct {
	t   *testing.T
	s   *Store
	dir string
	id  string
	n   int
}

func newUpRig(t *testing.T, spec string) *upRig {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := form.Normalize(json.RawMessage(spec))
	if err != nil {
		t.Fatal(err)
	}
	st, _, err := s.Create(context.Background(), CreateParams{Title: "Upload", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	return &upRig{t: t, s: s, dir: dir, id: st.ID}
}

// send uploads data as a file of `pieces` chunks, in the order given (nil:
// 0…n-1) and returns the record the last chunk produced.
func (r *upRig) send(key, name string, data []byte, pieces int, order []int) (*form.FileInfo, string, error) {
	r.t.Helper()
	r.n++
	session := fmt.Sprintf("sess%05d", r.n)
	return r.sendAs(session, key, name, data, pieces, order)
}

func (r *upRig) sendAs(session, key, name string, data []byte, pieces int, order []int) (*form.FileInfo, string, error) {
	r.t.Helper()
	size := (len(data) + pieces - 1) / pieces
	if order == nil {
		for i := 0; i < pieces; i++ {
			order = append(order, i)
		}
	}
	var info *form.FileInfo
	var ref string
	var err error
	for _, i := range order {
		lo, hi := i*size, min(len(data), (i+1)*size)
		info, ref, err = r.s.PutChunk(Chunk{Form: r.id, Key: key, Session: session, Index: i, Total: pieces, Name: name, Data: data[lo:hi]})
		if err != nil {
			return nil, "", err
		}
	}
	return info, ref, nil
}

func kindOf(err error) UploadKind {
	var ue *UploadError
	if errors.As(err, &ue) {
		return ue.Kind
	}
	return 0
}

func TestUploadAssemblesOutOfOrder(t *testing.T) {
	r := newUpRig(t, `[{"key":"doc","type":"file","accept":"image/*"}]`)
	data := append(append([]byte{}, pngHead...), bytes.Repeat([]byte("pixels"), 500)...)
	sum := sha256.Sum256(data)

	// The first chunks leave it pending: nothing returned, nothing final on disk.
	info, _, err := r.sendAs("sessabcd1", "doc", "shot.png", data, 4, []int{2, 0})
	if err != nil || info != nil {
		t.Fatalf("partial: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "uploads", r.id, "doc")); err == nil {
		t.Fatal("a final dir exists before the file is complete")
	}
	info, ref, err := r.sendAs("sessabcd1", "doc", "shot.png", data, 4, []int{3, 1, 1}) // a chunk twice
	if err != nil || info == nil {
		t.Fatalf("complete: %v %v", info, err)
	}
	if !form.ValidRef(ref) || info.Name != "shot.png" || info.Size != int64(len(data)) ||
		info.ContentType != "image/png" || info.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("record: %+v %s", info, ref)
	}
	want := filepath.Join(r.dir, "uploads", r.id, "doc", "shot.png")
	if info.Path != want {
		t.Fatalf("path %q, want %q", info.Path, want)
	}
	if b, _ := os.ReadFile(info.Path); !bytes.Equal(b, data) {
		t.Fatal("assembled bytes differ")
	}
	if fi, _ := os.Stat(info.Path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(r.dir, "uploads", r.id, "tmp", "doc", "sessabcd1")); err == nil {
		t.Fatal("chunks left behind")
	}
	// The ref resolves, only for this form and field.
	if got, ok := r.s.Upload(r.id, "doc", ref); !ok || got != *info {
		t.Fatalf("Upload: %v %v", got, ok)
	}
	if _, ok := r.s.Upload(r.id, "other", ref); ok {
		t.Fatal("ref resolved under another key")
	}
	if _, ok := r.s.Upload("zzz_aaaaaa", "doc", ref); ok {
		t.Fatal("ref resolved under another form")
	}
	for _, bad := range []string{"../../x", "upl_short", "", "upl_AAAAAAAAAAAA"} {
		if _, ok := r.s.Upload(r.id, "doc", bad); ok {
			t.Fatalf("bad ref %q resolved", bad)
		}
	}
	// A retry of the last chunk after it is done (the answer got lost): the
	// same record, not a second file.
	again, ref2, err := r.sendAs("sessabcd1", "doc", "shot.png", data, 4, []int{3})
	if err != nil || again == nil || ref2 != ref || again.Path != info.Path {
		t.Fatalf("retry after done: %v %v %v", again, ref2, err)
	}
	// Same name, new upload: gets its own file.
	info2, _, _ := r.send("doc", "shot.png", data, 1, nil)
	if info2.Name != "shot-2.png" || info2.Path == info.Path {
		t.Fatalf("name clash: %+v", info2)
	}
}

func TestUploadRefusals(t *testing.T) {
	r := newUpRig(t, `[
	  {"key":"img","type":"file","accept":"image/*,.pdf","maxMB":0.001},
	  {"key":"any","type":"file"},
	  {"key":"note","type":"text"}]`)
	png := append([]byte{}, pngHead...)

	// Wrong kind by its bytes, whatever the name says.
	if _, _, err := r.send("img", "notes.png", []byte("just some text, not a png at all"), 1, nil); kindOf(err) != UploadType {
		t.Fatalf("text as png: %v", err)
	}
	if _, _, err := r.send("img", "fake.pdf", []byte("just some text"), 1, nil); kindOf(err) != UploadType {
		t.Fatalf("text as pdf: %v", err)
	}
	if info, _, err := r.send("img", "real.pdf", []byte("%PDF-1.7\n%%EOF\n"), 1, nil); err != nil || info.ContentType != "application/pdf" {
		t.Fatalf("pdf: %v %v", info, err)
	}
	// Over maxMB (0.001 MB = 1048 bytes): caught while the chunks pile up.
	big := append(png, bytes.Repeat([]byte{1}, 2000)...)
	if _, _, err := r.send("img", "big.png", big, 4, nil); kindOf(err) != UploadTooBig {
		t.Fatalf("oversize: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(r.dir, "uploads", r.id, "tmp", "img", "*")); len(left) != 0 {
		t.Fatalf("oversize left chunks: %v", left)
	}
	// Anything goes on a field without accept.
	if _, _, err := r.send("any", "blob.bin", []byte{1, 2, 3, 0xff}, 1, nil); err != nil {
		t.Fatalf("any: %v", err)
	}
	// Not a file field, unknown field, unknown form, malformed chunks.
	cases := map[string]Chunk{
		"text field":  {Form: r.id, Key: "note", Session: "sessionaa", Index: 0, Total: 1, Name: "a", Data: []byte("x")},
		"no field":    {Form: r.id, Key: "nope", Session: "sessionaa", Index: 0, Total: 1, Name: "a", Data: []byte("x")},
		"no form":     {Form: "nope_aaaaaa", Key: "any", Session: "sessionaa", Index: 0, Total: 1, Name: "a", Data: []byte("x")},
		"bad session": {Form: r.id, Key: "any", Session: "../..", Index: 0, Total: 1, Name: "a", Data: []byte("x")},
		"bad key":     {Form: r.id, Key: "../any", Session: "sessionaa", Index: 0, Total: 1, Name: "a", Data: []byte("x")},
		"index>=n":    {Form: r.id, Key: "any", Session: "sessionaa", Index: 2, Total: 2, Name: "a", Data: []byte("x")},
		"negative":    {Form: r.id, Key: "any", Session: "sessionaa", Index: -1, Total: 2, Name: "a", Data: []byte("x")},
		"no chunks":   {Form: r.id, Key: "any", Session: "sessionaa", Index: 0, Total: 0, Name: "a", Data: []byte("x")},
		"too many":    {Form: r.id, Key: "any", Session: "sessionaa", Index: 0, Total: MaxChunks + 1, Name: "a", Data: []byte("x")},
		"empty":       {Form: r.id, Key: "any", Session: "sessionaa", Index: 0, Total: 1, Name: "a"},
		"huge chunk":  {Form: r.id, Key: "any", Session: "sessionaa", Index: 0, Total: 1, Name: "a", Data: make([]byte, UploadChunk+1)},
	}
	for name, c := range cases {
		if _, _, err := r.s.PutChunk(c); kindOf(err) != UploadBad {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUploadNamesStayInTheFieldDirectory(t *testing.T) {
	r := newUpRig(t, `[{"key":"any","type":"file"}]`)
	root := filepath.Join(r.dir, "uploads", r.id, "any")
	for name, want := range map[string]string{
		"../../../../etc/passwd": "passwd", `..\..\boot.ini`: "boot.ini", "..": "file", "/etc/cron.d/x": "x", ".bashrc": "bashrc",
		"a\x00b": "a_b",
	} {
		info, _, err := r.send("any", name, []byte("data"), 1, nil)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if filepath.Dir(info.Path) != root || info.Name != want {
			t.Errorf("%q → %q in %s, want %q in %s", name, info.Name, filepath.Dir(info.Path), want, root)
		}
	}
}

func TestUploadQuotas(t *testing.T) {
	r := newUpRig(t, `[{"key":"a","type":"file","maxMB":50}]`)
	chunk := bytes.Repeat([]byte{9}, UploadChunk)
	put := func(session string, i, n int) error {
		_, _, err := r.s.PutChunk(Chunk{Form: r.id, Key: "a", Session: session, Index: i, Total: n, Name: "x.bin", Data: chunk})
		return err
	}
	// 30 MB finished, then a second file runs into the form's 50 MB.
	for i := 0; i < 30; i++ {
		if err := put("sessionone", i, 30); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
	var err error
	for i := 0; i < 30 && err == nil; i++ {
		err = put("sessiontwo", i, 30)
	}
	if kindOf(err) != UploadTooBig {
		t.Fatalf("quota: %v", err)
	}
	// Chunks of an abandoned upload count too: they are bytes on disk.
	r = newUpRig(t, `[{"key":"a","type":"file","maxMB":50}]`)
	for s := 0; s < 6; s++ {
		for i := 0; i < 10; i++ {
			err = put(fmt.Sprintf("sess%05d", s), i, 64)
			if err != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if kindOf(err) != UploadTooBig {
		t.Fatalf("staged bytes should count: %v", err)
	}
}

func TestUploadFileCountCap(t *testing.T) {
	r := newUpRig(t, `[{"key":"a","type":"file","multiple":true}]`)
	for i := 0; i < MaxFormFiles; i++ {
		if _, _, err := r.send("a", fmt.Sprintf("f%d.txt", i), []byte("x"), 1, nil); err != nil {
			t.Fatalf("file %d: %v", i, err)
		}
	}
	if _, _, err := r.send("a", "one-more.txt", []byte("x"), 1, nil); kindOf(err) != UploadTooBig {
		t.Fatalf("21st file: %v", err)
	}
}

func TestRemoveUpload(t *testing.T) {
	r := newUpRig(t, `[{"key":"a","type":"file"}]`)
	info, ref, _ := r.send("a", "x.txt", []byte("hello"), 1, nil)
	if err := r.s.RemoveUpload(r.id, "a", ref); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); err == nil {
		t.Fatal("file still there")
	}
	if _, ok := r.s.Upload(r.id, "a", ref); ok {
		t.Fatal("ref still resolves")
	}
	if err := r.s.RemoveUpload(r.id, "a", ref); err != nil {
		t.Fatalf("removing twice: %v", err)
	}
	r.s.Close(r.id, Revoked, nil, nil)
	if err := r.s.RemoveUpload(r.id, "a", ref); kindOf(err) != UploadClosed {
		t.Fatalf("after close: %v", err)
	}
}

func TestUploadsSettleWhenTheFormCloses(t *testing.T) {
	for _, typ := range []string{Submitted, Declined, Revoked} {
		r := newUpRig(t, `[{"key":"a","type":"file","multiple":true}]`)
		keep, _, _ := r.send("a", "keep.txt", []byte("keep me"), 1, nil)
		r.send("a", "drop.txt", []byte("drop me"), 1, nil)
		r.sendAs("sesshalf1", "a", "half.txt", []byte("0123456789"), 2, []int{0}) // never finished
		var answers map[string]any
		if typ == Submitted {
			answers = map[string]any{"a": []form.FileInfo{*keep}}
		}
		if _, err := r.s.Close(r.id, typ, answers, nil); err != nil {
			t.Fatal(err)
		}
		left := filesUnder(filepath.Join(r.dir, "uploads", r.id))
		if typ == Submitted {
			if len(left) != 1 || left[0] != keep.Path {
				t.Fatalf("submitted keeps only the answer's file: %v", left)
			}
			if b, _ := os.ReadFile(keep.Path); string(b) != "keep me" {
				t.Fatal("kept file damaged")
			}
		} else if len(left) != 0 {
			t.Fatalf("%s should leave nothing: %v", typ, left)
		}
		// Chunks arriving after the close are refused.
		if _, _, err := r.sendAs("sesshalf1", "a", "half.txt", []byte("0123456789"), 2, []int{1}); kindOf(err) != UploadClosed {
			t.Fatalf("%s: chunk after close: %v", typ, err)
		}
	}
}

func TestPurgeUploads(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	dir := t.TempDir()
	s, _ := Open(dir)
	s.now = func() time.Time { return now }
	mk := func(title string, ttl time.Duration) State {
		f, _ := form.Normalize(json.RawMessage(`[{"key":"a","type":"file","multiple":true}]`))
		st, _, err := s.Create(context.Background(), CreateParams{Title: title, Fields: f, TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	put := func(id, session, name string, pieces, upto int) *form.FileInfo {
		var info *form.FileInfo
		for i := 0; i < upto; i++ {
			info, _, _ = s.PutChunk(Chunk{Form: id, Key: "a", Session: session, Index: i, Total: pieces, Name: name, Data: []byte("data")})
		}
		return info
	}

	open := mk("open", time.Hour*48)
	put(open.ID, "freshsess1", "fresh.txt", 2, 1)
	put(open.ID, "stalesess1", "stale.txt", 2, 1)
	// Files on disk keep the real clock: set the ages the purge will see.
	age := func(session string, d time.Duration) {
		at := now.Add(2 * time.Hour).Add(-d)
		os.Chtimes(filepath.Join(dir, "uploads", open.ID, "tmp", "a", session), at, at)
	}
	age("freshsess1", 10*time.Minute)
	age("stalesess1", 2*time.Hour)
	done := put(open.ID, "donesess01", "done.txt", 1, 1)

	short := mk("short", time.Hour)
	shortFile := put(short.ID, "shortsess1", "s.txt", 1, 1)

	sub := mk("sub", time.Hour*48)
	subFile := put(sub.ID, "subsess001", "sub.txt", 1, 1)
	s.Close(sub.ID, Submitted, map[string]any{"a": []form.FileInfo{*subFile}}, nil)

	// A directory of a form this store doesn't know.
	os.MkdirAll(filepath.Join(dir, "uploads", "ghost_aaaaaa", "a"), 0o700)
	os.WriteFile(filepath.Join(dir, "uploads", "ghost_aaaaaa", "a", "x"), []byte("x"), 0o600)

	s.now = func() time.Time { return now.Add(2 * time.Hour) } // `short` has expired
	s.PurgeUploads()
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	if !exists(filepath.Join(dir, "uploads", open.ID, "tmp", "a", "freshsess1")) {
		t.Error("a fresh half-sent file was purged")
	}
	if exists(filepath.Join(dir, "uploads", open.ID, "tmp", "a", "stalesess1")) {
		t.Error("a stale half-sent file stayed")
	}
	if !exists(done.Path) {
		t.Error("a finished file of an open form was purged")
	}
	if exists(shortFile.Path) {
		t.Error("an expired form kept its files")
	}
	if !exists(subFile.Path) {
		t.Error("a submitted form lost its file early")
	}
	if exists(filepath.Join(dir, "uploads", "ghost_aaaaaa")) {
		t.Error("an unknown form's uploads stayed")
	}
	// A week after the submit the files go.
	s.now = func() time.Time { return now.Add(UploadTTL + 3*time.Hour) }
	s.PurgeUploads()
	if exists(subFile.Path) || exists(filepath.Join(dir, "uploads", sub.ID)) {
		t.Error("submitted files outlived UploadTTL")
	}
}

func TestMultiFormRefusesFileFields(t *testing.T) {
	s, _ := Open(t.TempDir())
	f, _ := form.Normalize(json.RawMessage(`[{"key":"a","type":"file"}]`))
	_, _, err := s.Create(context.Background(), CreateParams{Title: "Vote", Fields: f, Multi: true})
	if err == nil || !strings.Contains(err.Error(), "file") || !strings.Contains(err.Error(), "disk") {
		t.Fatalf("multi + file: %v", err)
	}
}

func filesUnder(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}
