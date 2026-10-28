package form

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeFile(t *testing.T) {
	f := norm(t, `[{"key":"shot","type":"file","label":"Screenshot","default":"x"}]`)
	got := f[0]
	if got.Type != File || got.MaxMB != FileMaxMBDefault || got.Multiple || got.FileLimit() != 1 || got.Accept != "" || got.Default != nil {
		t.Fatalf("defaults: %+v", got)
	}
	f = norm(t, `[{"key":"shots","type":"file","accept":" Image/* , .PDF ,application/pdf","maxMB":0.5,"multiple":true,"maxFiles":3}]`)
	got = f[0]
	if got.Accept != "image/*,.pdf,application/pdf" || got.MaxMB != 0.5 || got.MaxBytes() != 500000 || !got.Multiple || got.FileLimit() != 3 {
		t.Fatalf("explicit: %+v", got)
	}
	// maxFiles means nothing without multiple.
	if f = norm(t, `[{"key":"a","type":"file","maxFiles":5}]`); f[0].FileLimit() != 1 {
		t.Fatalf("maxFiles without multiple: %+v", f[0])
	}
	for _, bad := range []string{
		`[{"key":"a","type":"file","accept":"image"}]`,
		`[{"key":"a","type":"file","accept":"*/*"}]`,
		`[{"key":"a","type":"file","accept":".a b"}]`,
		`[{"key":"a","type":"file","maxMB":0}]`,
		`[{"key":"a","type":"file","maxMB":51}]`,
		`[{"key":"a","type":"file","multiple":true,"maxFiles":11}]`,
		`[{"key":"a","type":"file","multiple":true,"maxFiles":1.5}]`,
		`[{"key":"a","type":"file","accept":"` + strings.Repeat(".a,", MaxAcceptTokens+1) + `"}]`,
	} {
		if _, err := Normalize(json.RawMessage(bad)); err == nil {
			t.Errorf("want an error for %s", bad)
		}
	}
}

func TestParseFile(t *testing.T) {
	fields := norm(t, `[{"key":"shot","type":"file","required":true},{"key":"more","type":"file","multiple":true,"maxFiles":2}]`)
	have := map[string]FileInfo{
		"upl_aaaaaaaaaaaa": {Name: "a.png", Size: 1500, ContentType: "image/png", SHA256: "x", Path: "/d/a.png"},
		"upl_bbbbbbbbbbbb": {Name: "b.pdf", Size: 20, Path: "/d/b.pdf"},
		"upl_cccccccccccc": {Name: "c.txt", Size: 3000000, Path: "/d/c.txt"},
	}
	h := Hooks{File: func(key, ref string) (FileInfo, bool) { i, ok := have[ref]; return i, ok }}
	parse := func(v url.Values, hk Hooks) Result { return ParseWith(fields, v, hk) }

	r := parse(url.Values{"f.shot": {`["upl_aaaaaaaaaaaa"]`}, "f.more": {`["upl_bbbbbbbbbbbb","upl_cccccccccccc","upl_bbbbbbbbbbbb"]`}}, h)
	if !r.OK() {
		t.Fatalf("valid: %v", r.Errors)
	}
	shot := r.Answers["shot"].([]FileInfo)
	if len(shot) != 1 || shot[0].Path != "/d/a.png" {
		t.Fatalf("answer: %v", r.Answers["shot"])
	}
	if more := r.Answers["more"].([]FileInfo); len(more) != 2 { // the repeated ref counts once
		t.Fatalf("dedupe: %v", more)
	}
	if got := AnswerText(fields[1], r.Answers["more"]); got != "b.pdf (20 B), c.txt (3.0 MB)" {
		t.Fatalf("recap: %q", got)
	}
	// What a stored answer looks like when read back from the events file.
	b, _ := json.Marshal(r.Answers["shot"])
	var back any
	json.Unmarshal(b, &back)
	if got := AnswerText(fields[0], back); got != "a.png (2 KB)" {
		t.Fatalf("recap of stored answer: %q", got)
	}

	for name, c := range map[string]struct {
		v    url.Values
		hook Hooks
		msg  string
	}{
		"required":     {url.Values{}, h, "Add a file."},
		"empty list":   {url.Values{"f.shot": {`[]`}}, h, "Add a file."},
		"not json":     {url.Values{"f.shot": {`aaa`}}, h, "Add the file again."},
		"bad ref":      {url.Values{"f.shot": {`["../../etc/passwd"]`}}, h, "Add the file again."},
		"unknown ref":  {url.Values{"f.shot": {`["upl_zzzzzzzzzzzz"]`}}, h, "gone"},
		"too many":     {url.Values{"f.shot": {`["upl_aaaaaaaaaaaa","upl_bbbbbbbbbbbb"]`}}, h, "At most 1 file."},
		"no uploading": {url.Values{"f.shot": {`["upl_aaaaaaaaaaaa"]`}}, Hooks{}, "aren't available"},
	} {
		r := parse(c.v, c.hook)
		if r.OK() || !strings.Contains(r.Errors["shot"], c.msg) {
			t.Errorf("%s: want %q, got %v", name, c.msg, r.Errors)
		}
	}
	// An optional field left empty is just absent.
	r = parse(url.Values{"f.shot": {`["upl_aaaaaaaaaaaa"]`}}, h)
	if _, ok := r.Answers["more"]; !ok == false || !r.OK() {
		t.Fatalf("optional: %v %v", r.Answers, r.Errors)
	}
}

func TestAcceptMatch(t *testing.T) {
	for _, c := range []struct {
		accept, name, sniffed string
		want                  bool
	}{
		{"", "anything.bin", "application/octet-stream", true},
		{"image/*", "a.png", "image/png", true},
		{"image/*", "a.png", "text/plain", false},
		{"image/*", "a.heic", "image/heic", true},
		{"application/pdf", "x.pdf", "application/pdf", true},
		{"application/pdf", "x.pdf", "text/plain", false},
		{".pdf", "X.PDF", "application/pdf", true},
		{".pdf", "x.pdf", "text/plain", false}, // renamed
		{".png,.jpg", "p.jpg", "image/jpeg", true},
		{".png", "p.jpg", "image/jpeg", false},
		{".csv", "d.csv", "text/plain", true},
		{".csv", "d.csv", "application/octet-stream", false},
		{".docx", "d.docx", "application/zip", true},
		{".exe", "a.exe", "application/octet-stream", true}, // unknown extension: taken at its word
		{"text/csv", "d.csv", "text/plain", true},
		{"application/json", "d.json", "text/plain", true},
		{"application/json", "d.json", "image/png", false},
		{"text/*", "n.md", "text/plain", true},
		{"video/*", "v.mov", "video/quicktime", true},
	} {
		if got := AcceptMatch(c.accept, c.name, c.sniffed); got != c.want {
			t.Errorf("AcceptMatch(%q, %q, %q) = %v, want %v", c.accept, c.name, c.sniffed, got, c.want)
		}
	}
}

func TestSafeFileName(t *testing.T) {
	long := strings.Repeat("é", 80) + ".jpeg"
	for in, want := range map[string]string{
		"photo.png":                      "photo.png",
		"../../etc/passwd":               "passwd",
		`..\..\Windows\system.ini`:       "system.ini",
		"/abs/path/file.txt":             "file.txt",
		"..":                             "file",
		".":                              "file",
		".hidden":                        "hidden",
		"":                               "file",
		"a\x00b\nc.txt":                  "a_b_c.txt",
		"rep‮ort.pdf":                    "rep_ort.pdf",
		"my  cool\tfile .txt":            "my cool_file .txt",
		`we<ir>d:na"me|?*.txt`:           "we_ir_d_na_me___.txt",
		"$(rm -rf).sh":                   "_(rm -rf).sh",
		"  trailing dots...  ":           "trailing dots",
		"Capture d'écran 2026-10-28.png": "Capture d'écran 2026-10-28.png",
	} {
		if got := SafeFileName(in); got != want {
			t.Errorf("SafeFileName(%q) = %q, want %q", in, got, want)
		}
	}
	got := SafeFileName(long)
	if len(got) > MaxFileNameBytes || !strings.HasSuffix(got, ".jpeg") || !strings.HasPrefix(got, "é") {
		t.Errorf("long name: %q (%d bytes)", got, len(got))
	}
}

func TestSniffType(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 40)...)
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 20)...)
	qt := append([]byte{0, 0, 0, 20}, []byte("ftypqt  ")...)
	qt = append(qt, make([]byte, 20)...)
	for name, c := range map[string]struct {
		b    []byte
		want string
	}{
		"png":   {png, "image/png"},
		"heic":  {heic, "image/heic"},
		"mov":   {qt, "video/quicktime"},
		"text":  {[]byte("hello, world\n"), "text/plain"},
		"pdf":   {[]byte("%PDF-1.7\n"), "application/pdf"},
		"bytes": {[]byte{0x01, 0x02, 0x03, 0xff, 0xfe}, "application/octet-stream"},
	} {
		if got := SniffType(c.b); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
