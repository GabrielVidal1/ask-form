package form

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/GabrielVidal1/ask-form/internal/i18n"
)

// The `file` field: the browser uploads each file in chunks to the form's own
// upload endpoint (internal/server/upload.go) as soon as it is picked and the
// form post then carries the refs of the finished uploads, never bytes. The
// answer is the list of files with their path on the machine running
// `ask-form serve`.

// Limits of a file field.
const (
	FileMaxMBDefault = 10 // one file, unless the spec says maxMB
	FileMaxMB        = 50 // …and the ceiling: it is also the whole form's
	FileMaxFiles     = 10 // per field
	MaxAcceptTokens  = 20
	MaxFileNameBytes = 100
)

// FileInfo is one uploaded file as an answer carries it.
type FileInfo struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
	SHA256      string `json:"sha256"`
	Path        string `json:"path"`
}

// Hooks lets Parse reach what the form package can't see. File resolves the
// ref of an upload a respondent made to this form's field; nil: uploads are
// not available (a demo), so a file field can't be answered.
type Hooks struct {
	File func(key, ref string) (FileInfo, bool)
	// Lang is the language of the messages (internal/i18n; "" = English).
	Lang string
}

var (
	acceptTokenRe = regexp.MustCompile(`^(\.[a-z0-9][a-z0-9+_-]{0,15}|[a-z]+/(\*|[a-z0-9][a-z0-9.+_-]*))$`)
	refRe         = regexp.MustCompile(`^upl_[a-z0-9]{12}$`)
)

// ValidRef reports whether s looks like an upload ref (`upl_` + 12 chars).
func ValidRef(s string) bool { return refRe.MatchString(s) }

// MaxBytes is the size one file of the field may reach: maxMB megabytes, the
// decimal ones a file manager shows.
func (f Field) MaxBytes() int64 { return int64(f.MaxMB * 1_000_000) }

// FileLimit is how many files the field takes.
func (f Field) FileLimit() int {
	if f.MaxFiles > 0 {
		return f.MaxFiles
	}
	return 1
}

// fileSpec reads the `file` knobs of a field: accept, maxMB, multiple, maxFiles.
func fileSpec(f *Field, m map[string]any) error {
	acc, err := normalizeAccept(str(m["accept"]))
	if err != nil {
		return err
	}
	f.Accept = acc
	f.MaxMB = FileMaxMBDefault
	if v, ok := num(m["maxMB"]); ok {
		if v <= 0 || v > FileMaxMB {
			return fmt.Errorf("maxMB: between 0 and %d", FileMaxMB)
		}
		f.MaxMB = math.Round(v*1000) / 1000
	}
	f.Multiple = truthy(m["multiple"])
	f.MaxFiles = 1
	if f.Multiple {
		f.MaxFiles = FileMaxFiles
		if v, ok := num(m["maxFiles"]); ok {
			if v < 1 || v > FileMaxFiles || v != math.Trunc(v) {
				return fmt.Errorf("maxFiles: a whole number between 1 and %d", FileMaxFiles)
			}
			f.MaxFiles = int(v)
		}
	}
	return nil
}

// normalizeAccept lower-cases an `accept` list (the <input accept> syntax:
// `image/*`, `application/pdf`, `.csv`) and checks each entry.
func normalizeAccept(s string) (string, error) {
	var out []string
	for _, tok := range strings.Split(strings.ToLower(s), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if !acceptTokenRe.MatchString(tok) {
			return "", fmt.Errorf("accept: %q is not a media type (image/*, application/pdf) or an extension (.csv)", tok)
		}
		out = append(out, tok)
	}
	if len(out) > MaxAcceptTokens {
		return "", fmt.Errorf("accept: at most %d entries", MaxAcceptTokens)
	}
	return strings.Join(out, ","), nil
}

// extKinds: the sniffed type a file with this extension must have. Text
// formats are checked with the "text/" prefix (text/plain for source, csv,
// json… text/xml, text/html), zip-based office formats as application/zip.
// An extension not listed is taken at its word.
var extKinds = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".bmp": "image/bmp", ".pdf": "application/pdf",
	".heic": "image/heic", ".heif": "image/heif", ".avif": "image/avif",
	".zip": "application/zip", ".docx": "application/zip", ".xlsx": "application/zip",
	".pptx": "application/zip", ".odt": "application/zip", ".ods": "application/zip", ".jar": "application/zip",
	".gz": "application/x-gzip", ".mp3": "audio/mpeg", ".wav": "audio/wave", ".mp4": "video/mp4",
	".webm": "video/webm", ".mov": "video/quicktime",
}

var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".csv": true, ".tsv": true, ".log": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true, ".htm": true, ".css": true,
	".js": true, ".mjs": true, ".ts": true, ".tsx": true, ".jsx": true, ".py": true, ".go": true, ".rs": true,
	".sh": true, ".sql": true, ".svg": true, ".ini": true, ".env": true, ".diff": true, ".patch": true,
	".ndjson": true, ".jsonl": true, ".rb": true, ".java": true, ".c": true, ".h": true, ".cpp": true,
}

// textTypes are media types whose bytes sniff as text.
var textTypes = map[string]bool{
	"application/json": true, "application/xml": true, "application/x-yaml": true, "application/yaml": true,
	"application/javascript": true, "application/x-ndjson": true, "application/toml": true,
	"application/sql": true, "image/svg+xml": true,
}

// SniffType is the media type of a file's first bytes (up to 512 are looked
// at): net/http's sniffing plus the HEIC / AVIF / QuickTime boxes phones make.
func SniffType(head []byte) string {
	t := http.DetectContentType(head)
	if t == "application/octet-stream" && len(head) >= 12 && string(head[4:8]) == "ftyp" {
		switch string(head[8:12]) {
		case "heic", "heix", "hevc", "heim", "heis":
			return "image/heic"
		case "mif1", "msf1":
			return "image/heif"
		case "avif", "avis":
			return "image/avif"
		case "qt  ":
			return "video/quicktime"
		case "M4A ", "M4B ":
			return "audio/mp4"
		}
	}
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	return t
}

// AcceptMatch reports whether a file named name whose bytes sniff as sniffed
// is allowed by the field's accept list; an empty list allows anything. A
// media type is matched against the sniffed one, an extension against the
// name — and, for the formats it knows, against the bytes too, so a renamed
// file doesn't pass.
func AcceptMatch(accept, name, sniffed string) bool {
	if accept == "" {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	text := strings.HasPrefix(sniffed, "text/")
	for _, tok := range strings.Split(accept, ",") {
		switch {
		case strings.HasPrefix(tok, "."):
			if tok == ext && extFits(ext, sniffed) {
				return true
			}
		case strings.HasSuffix(tok, "/*"):
			if strings.HasPrefix(sniffed, strings.TrimSuffix(tok, "*")) {
				return true
			}
		case tok == sniffed, text && (strings.HasPrefix(tok, "text/") || textTypes[tok]):
			return true
		}
	}
	return false
}

func extFits(ext, sniffed string) bool {
	if want, ok := extKinds[ext]; ok {
		return sniffed == want
	}
	if textExts[ext] {
		return strings.HasPrefix(sniffed, "text/")
	}
	return true
}

// SafeFileName turns the name a browser gave into one fit for a directory
// entry: its base name only, no control characters or separators, no leading
// dot, at most MaxFileNameBytes with the extension kept. Never empty.
func SafeFileName(raw string) string {
	if i := strings.LastIndexAny(raw, `/\`); i >= 0 {
		raw = raw[i+1:]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r == utf8.RuneError, unicode.IsControl(r), r == '‮', r == '⁦', r == '⁧', r == '⁨', r == '⁩':
			b.WriteByte('_')
		case strings.ContainsRune(`<>:"|?*%$`+"`", r):
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if name == "" {
		return "file"
	}
	if len(name) > MaxFileNameBytes {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		stem := clip(strings.TrimSuffix(name, ext), MaxFileNameBytes-len(ext))
		name = strings.TrimRight(stem, ". ") + ext
	}
	return name
}

// fileRefs reads the hidden value of a file field: a JSON array of refs. The
// second result is the message for a value that isn't one.
func fileRefs(s string, t func(string, ...any) string) ([]string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ""
	}
	var refs []string
	if json.Unmarshal([]byte(s), &refs) != nil {
		return nil, t("Add the file again.")
	}
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		if !ValidRef(r) {
			return nil, t("Add the file again.")
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out, ""
}

// FileText renders a file answer for a recap: "photo.png (1.2 MB), notes.pdf (80 KB)".
func FileText(v any) string {
	var parts []string
	add := func(name string, size float64) {
		parts = append(parts, name+" ("+HumanSize(int64(size))+")")
	}
	switch t := v.(type) {
	case []FileInfo:
		for _, f := range t {
			add(f.Name, float64(f.Size))
		}
	case []any:
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				n, _ := m["name"].(string)
				sz, _ := m["size"].(float64)
				add(n, sz)
			}
		}
	}
	return strings.Join(parts, ", ")
}

// HumanSize prints a byte count as 800 B, 12 KB, 3.4 MB.
func HumanSize(n int64) string { return HumanSizeIn(i18n.EN, n) }

// HumanSizeIn is HumanSize in lang (internal/i18n): "1,5 Mo" in French.
func HumanSizeIn(lang string, n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d %s", n, i18n.T(lang, "B"))
	case n < 1_000_000:
		return fmt.Sprintf("%d %s", (n+500)/1000, i18n.T(lang, "KB"))
	}
	s := fmt.Sprintf("%.1f", float64(n)/1e6)
	if lang == i18n.FR {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s + " " + i18n.T(lang, "MB")
}
