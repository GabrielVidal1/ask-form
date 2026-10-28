package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

// Uploads, on the machine running `serve`:
//
//	<dir>/uploads/<id>/tmp/<key>/<session>/<n>.part   chunks of a file on its way
//	<dir>/uploads/<id>/<key>/<name>                   a finished file
//	<dir>/uploads/<id>/<key>/.<ref>.json              its record (ref → file), until the form closes
//
// A browser sends a file as chunks under a session id it makes up; the last
// chunk to arrive assembles them, the bytes are sniffed against the field's
// `accept`, the SHA-256 is taken and the file gets a ref (`upl_…`). The form
// post carries refs; once it is submitted the files it names stay (UploadTTL)
// and everything else goes. A form that closes any other way, or expires,
// loses all of them.

// Limits.
const (
	UploadChunk   = 1 << 20    // the largest chunk
	MaxFormUpload = 50_000_000 // finished files plus chunks waiting, per form (50 MB)
	MaxFormFiles  = 20         // finished files per form
	MaxChunks     = 64         // chunks per file
	UploadTTL     = 7 * 24 * time.Hour
	stagingTTL    = time.Hour // a half-sent file is dropped after this
)

// UploadKind says why an upload was refused, for the HTTP status.
type UploadKind int

const (
	UploadBad    UploadKind = iota + 1 // malformed request
	UploadClosed                       // the form is not open
	UploadTooBig                       // over the field's maxMB or the form's quota
	UploadType                         // not what `accept` allows
)

// UploadError is a refused upload; its message is for the respondent.
type UploadError struct {
	Kind UploadKind
	Msg  string
}

func (e *UploadError) Error() string { return e.Msg }

func badUpload(k UploadKind, format string, a ...any) error {
	return &UploadError{Kind: k, Msg: fmt.Sprintf(format, a...)}
}

var (
	sessionRe = regexp.MustCompile(`^[a-z0-9]{8,32}$`)
	keyRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// Chunk is one piece of a file.
type Chunk struct {
	Form, Key, Session string
	Index, Total       int // 0-based; Total pieces make the file
	Name               string
	Data               []byte
}

// uploadRecord is what is kept of a finished upload while its form is open.
type uploadRecord struct {
	Ref     string        `json:"ref"`
	Session string        `json:"session"`
	File    form.FileInfo `json:"file"`
}

func (s *Store) uploadsDir(id string) string { return filepath.Join(s.dir, "uploads", id) }

// PutChunk stores one chunk of a file. When it is the last one missing, the
// file is assembled and checked and its record is returned; nil, nil means
// more chunks are due. Sending a chunk again is harmless, even after the file
// is done (the record comes back), so a client can retry what it isn't sure of.
func (s *Store) PutChunk(c Chunk) (*form.FileInfo, string, error) {
	if !ValidID(c.Form) || !keyRe.MatchString(c.Key) || !sessionRe.MatchString(c.Session) {
		return nil, "", badUpload(UploadBad, "That upload didn't make sense — try again.")
	}
	if c.Total < 1 || c.Total > MaxChunks || c.Index < 0 || c.Index >= c.Total {
		return nil, "", badUpload(UploadBad, "That upload didn't make sense — try again.")
	}
	if len(c.Data) == 0 || len(c.Data) > UploadChunk {
		return nil, "", badUpload(UploadBad, "That upload didn't make sense — try again.")
	}
	st, err := s.Get(c.Form)
	if err != nil {
		return nil, "", badUpload(UploadBad, "That upload didn't make sense — try again.")
	}
	if st.Status != Pending {
		return nil, "", badUpload(UploadClosed, "This form is closed.")
	}
	var fld *form.Field
	for i := range st.Fields {
		if st.Fields[i].Key == c.Key && st.Fields[i].Type == form.File {
			fld = &st.Fields[i]
		}
	}
	if fld == nil {
		return nil, "", badUpload(UploadBad, "That upload didn't make sense — try again.")
	}

	s.upMu.Lock()
	defer s.upMu.Unlock()
	if rec := s.findBySession(c.Form, c.Key, c.Session); rec != nil {
		return &rec.File, rec.Ref, nil
	}
	root := s.uploadsDir(c.Form)
	sess := filepath.Join(root, "tmp", c.Key, c.Session)
	if err := os.MkdirAll(sess, 0o700); err != nil {
		return nil, "", err
	}
	part := filepath.Join(sess, strconv.Itoa(c.Index)+".part")
	var old int64
	if fi, err := os.Stat(part); err == nil {
		old = fi.Size()
	}
	if used := dirBytes(root) - old; used+int64(len(c.Data)) > MaxFormUpload {
		return nil, "", badUpload(UploadTooBig, "This form takes at most %s of files.", mb(MaxFormUpload))
	}
	if err := writeAtomic(part, c.Data); err != nil {
		return nil, "", err
	}
	parts, size := sessionParts(sess)
	if size > fld.MaxBytes() {
		os.RemoveAll(sess)
		return nil, "", badUpload(UploadTooBig, "That file is over the %s limit.", mb(fld.MaxBytes()))
	}
	for i := 0; i < c.Total; i++ {
		if !parts[i] {
			return nil, "", nil
		}
	}
	rec, err := s.assemble(root, sess, *fld, c)
	if err != nil {
		os.RemoveAll(sess)
		return nil, "", err
	}
	return &rec.File, rec.Ref, nil
}

// assemble joins a session's chunks into the field's directory.
func (s *Store) assemble(root, sess string, fld form.Field, c Chunk) (*uploadRecord, error) {
	head := make([]byte, 512)
	fh, err := os.Open(filepath.Join(sess, "0.part"))
	if err != nil {
		return nil, err
	}
	n, _ := io.ReadFull(fh, head)
	fh.Close()
	sniffed := form.SniffType(head[:n])
	name := form.SafeFileName(c.Name)
	if !form.AcceptMatch(fld.Accept, name, sniffed) {
		return nil, badUpload(UploadType, "That kind of file isn't accepted here%s.", acceptHint(fld.Accept))
	}
	if count := countFiles(root); count >= MaxFormFiles {
		return nil, badUpload(UploadTooBig, "This form takes at most %d files.", MaxFormFiles)
	}
	dir := filepath.Join(root, c.Key)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	dst, final, err := createUnique(dir, name)
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	var size int64
	fail := func(err error) (*uploadRecord, error) {
		dst.Close()
		os.Remove(final)
		return nil, err
	}
	for i := 0; i < c.Total; i++ {
		src, err := os.Open(filepath.Join(sess, strconv.Itoa(i)+".part"))
		if err != nil {
			return fail(err)
		}
		w, err := io.Copy(io.MultiWriter(dst, sum), src)
		src.Close()
		if err != nil {
			return fail(err)
		}
		size += w
	}
	if err := dst.Close(); err != nil {
		os.Remove(final)
		return nil, err
	}
	if size == 0 || size > fld.MaxBytes() {
		os.Remove(final)
		return nil, badUpload(UploadTooBig, "That file is over the %s limit.", mb(fld.MaxBytes()))
	}
	rec := &uploadRecord{
		Ref: newRef(), Session: c.Session,
		File: form.FileInfo{
			Name: filepath.Base(final), Size: size, ContentType: sniffed,
			SHA256: hex.EncodeToString(sum.Sum(nil)), Path: final,
		},
	}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, "."+rec.Ref+".json"), b, 0o600); err != nil {
		os.Remove(final)
		return nil, err
	}
	os.RemoveAll(sess)
	return rec, nil
}

// Upload resolves the ref of a file a respondent uploaded to the field of an
// open form.
func (s *Store) Upload(id, key, ref string) (form.FileInfo, bool) {
	if !ValidID(id) || !keyRe.MatchString(key) || !form.ValidRef(ref) {
		return form.FileInfo{}, false
	}
	b, err := os.ReadFile(filepath.Join(s.uploadsDir(id), key, "."+ref+".json"))
	if err != nil {
		return form.FileInfo{}, false
	}
	var rec uploadRecord
	if json.Unmarshal(b, &rec) != nil || rec.Ref != ref {
		return form.FileInfo{}, false
	}
	if fi, err := os.Stat(rec.File.Path); err != nil || fi.Size() != rec.File.Size {
		return form.FileInfo{}, false
	}
	return rec.File, true
}

// RemoveUpload deletes an upload before the form is sent (the respondent
// took the file out, or replaced it).
func (s *Store) RemoveUpload(id, key, ref string) error {
	st, err := s.Get(id)
	if err != nil || st.Status != Pending {
		return badUpload(UploadClosed, "This form is closed.")
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	info, ok := s.Upload(id, key, ref)
	if !ok {
		return nil // already gone
	}
	os.Remove(info.Path)
	os.Remove(filepath.Join(s.uploadsDir(id), key, "."+ref+".json"))
	return nil
}

func (s *Store) findBySession(id, key, session string) *uploadRecord {
	metas, _ := filepath.Glob(filepath.Join(s.uploadsDir(id), key, ".upl_*.json"))
	for _, p := range metas {
		var rec uploadRecord
		if b, err := os.ReadFile(p); err == nil && json.Unmarshal(b, &rec) == nil && rec.Session == session {
			return &rec
		}
	}
	return nil
}

// settleUploads is what a form's close does to its uploads: keep the files
// the answers name, drop the rest.
func (s *Store) settleUploads(id string, answers map[string]any) {
	root := s.uploadsDir(id)
	if _, err := os.Stat(root); err != nil {
		return
	}
	keep := answerPaths(answers)
	if len(keep) == 0 {
		os.RemoveAll(root)
		return
	}
	os.RemoveAll(filepath.Join(root, "tmp"))
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !keep[p] {
			os.Remove(p)
		}
		return nil
	})
	removeEmptyDirs(root)
}

// answerPaths lists the file paths a set of answers carries.
func answerPaths(answers map[string]any) map[string]bool {
	keep := map[string]bool{}
	for _, v := range answers {
		switch t := v.(type) {
		case []form.FileInfo:
			for _, f := range t {
				keep[f.Path] = true
			}
		case []any:
			for _, x := range t {
				if m, ok := x.(map[string]any); ok {
					if p, ok := m["path"].(string); ok && p != "" {
						keep[p] = true
					}
				}
			}
		}
	}
	return keep
}

// PurgeUploads drops what no form needs any more: half-sent files past an
// hour, the uploads of a form that is closed without them, expired or
// unknown, and the files of a submitted form after UploadTTL. `serve` runs
// it hourly (and at start).
func (s *Store) PurgeUploads() {
	entries, err := os.ReadDir(filepath.Join(s.dir, "uploads"))
	if err != nil {
		return
	}
	states := map[string]State{}
	for _, st := range s.List() {
		states[st.ID] = st
	}
	s.upMu.Lock()
	defer s.upMu.Unlock()
	now := s.now()
	for _, de := range entries {
		root := filepath.Join(s.dir, "uploads", de.Name())
		st, known := states[de.Name()]
		switch {
		case !known:
			os.RemoveAll(root)
		case st.Status == Pending:
			dropStale(filepath.Join(root, "tmp"), now)
		case st.Status == Submitted:
			if st.ClosedAt != nil && now.Sub(*st.ClosedAt) > UploadTTL {
				os.RemoveAll(root)
			} else {
				s.settleUploads(st.ID, st.Answers)
			}
		default:
			os.RemoveAll(root)
		}
	}
}

// dropStale removes the sessions of tmp that haven't been touched for stagingTTL.
func dropStale(tmp string, now time.Time) {
	keys, _ := os.ReadDir(tmp)
	for _, k := range keys {
		sessions, _ := os.ReadDir(filepath.Join(tmp, k.Name()))
		for _, se := range sessions {
			p := filepath.Join(tmp, k.Name(), se.Name())
			if fi, err := os.Stat(p); err == nil && now.Sub(fi.ModTime()) > stagingTTL {
				os.RemoveAll(p)
			}
		}
	}
	removeEmptyDirs(tmp)
}

// ── small helpers ────────────────────────────────────────────────────────────

func newRef() string {
	var b [12]byte
	rand.Read(b[:])
	for i := range b {
		b[i] = suffixAlphabet[int(b[i])%len(suffixAlphabet)]
	}
	return "upl_" + string(b[:])
}

// writeAtomic puts data at path through a temp file, so a chunk sent twice
// never leaves a torn one.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".chunk-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o600)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// sessionParts reports which chunks a session holds and their total size.
func sessionParts(sess string) (map[int]bool, int64) {
	have := map[int]bool{}
	var size int64
	ents, _ := os.ReadDir(sess)
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".part")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		if fi, err := e.Info(); err == nil {
			have[i] = true
			size += fi.Size()
		}
	}
	return have, size
}

// createUnique creates dir/name exclusively, adding -2, -3… before the
// extension when the name is taken.
func createUnique(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n < 1000; n++ {
		cand := name
		if n > 1 {
			cand = stem + "-" + strconv.Itoa(n) + ext
		}
		p := filepath.Join(dir, cand)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, p, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	return nil, "", errors.New("no free file name")
}

// dirBytes is the size of every file under dir.
func dirBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// countFiles counts the finished files of an upload root (their records).
func countFiles(root string) int {
	metas, _ := filepath.Glob(filepath.Join(root, "*", ".upl_*.json"))
	return len(metas)
}

func removeEmptyDirs(root string) {
	var dirs []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		os.Remove(d) // fails on a non-empty one, which is the point
	}
}

func mb(n int64) string { return form.HumanSize(n) }

func acceptHint(accept string) string {
	if accept == "" {
		return ""
	}
	return " (" + strings.ReplaceAll(accept, ",", ", ") + ")"
}
