package form

import (
	"fmt"
	"net/url"
	"strings"
)

// The note a respondent may leave when they decline a form: a text, files
// attached to it the way the "Anything else?" box takes them, or neither. It
// is no question — it never shows among the answers — but it is sent and its
// files go up as an attachable textarea's would, under a key no question has.

// declineKey is the note's key unless a question took it.
const declineKey = "decline"

// DeclineKey is where a decline's note goes: "decline", or "decline-2"… when
// a question has that key.
func DeclineKey(fields []Field) string {
	key, n := declineKey, 2
	for hasKey(fields, key) {
		key = fmt.Sprintf("%s-%d", declineKey, n)
		n++
	}
	return key
}

// DeclineNote is the note as a field: an optional textarea that takes files.
func DeclineNote(fields []Field) Field {
	return Field{Key: DeclineKey(fields), Type: Textarea}
}

// ParseDecline reads the note sent with a decline: its text ("" when only
// files came), the files, or a message for the respondent.
func ParseDecline(fields []Field, v url.Values, h Hooks) (string, []FileInfo, string) {
	f := DeclineNote(fields)
	res := ParseWith([]Field{f}, v, h)
	if msg := res.Errors[f.Key]; msg != "" {
		return "", nil, msg
	}
	infos, _ := res.Answers[FilesKey(f.Key)].([]FileInfo)
	text, _ := res.Answers[f.Key].(string)
	if strings.TrimSpace(v.Get(InputName(f.Key))) == "" {
		text = "" // the files' names stood in for it
	}
	return text, infos, ""
}
