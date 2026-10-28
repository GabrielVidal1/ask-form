package server

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	"github.com/GabrielVidal1/ask-form/internal/form"
	"github.com/GabrielVidal1/ask-form/internal/store"
)

// MaxChunk caps the body of an upload request: one chunk of a file. The relay
// lets this much through on the upload path only (everything else stays at
// MaxBody).
const MaxChunk = store.UploadChunk

// The upload endpoint, one POST per chunk:
//
//	POST /f/<id>/upload/<key>?c=<csrf>&u=<session>&i=<index>&n=<total>&name=<file name>
//	     Content-Type: application/octet-stream, the chunk as the body
//	  → 200 {"received":k,"total":n}                              more due
//	  → 200 {"done":true,"ref":"upl_…","name","size","contentType"}  the last one
//	POST /f/<id>/upload/<key>?c=<csrf>&rm=<ref>        take a finished upload out
//
// The metadata rides in the query, not in headers: only a short allow-list of
// headers crosses the relay's tunnel. (Never logged: see logged().) Only POST
// crosses it either, hence rm= instead of DELETE.
func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	ip := s.ip(r)
	if !s.uploads.hit(ip) {
		w.Header().Set("Retry-After", "60")
		uploadFail(w, http.StatusTooManyRequests, "Too many uploads — wait a minute and try again.")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		notFound(w)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		if u, err := url.Parse(o); err != nil || u.Host != r.Host {
			notFound(w)
			return
		}
	}
	q := r.URL.Query()
	if q.Has("token") {
		notFound(w) // a POST never carries the token
		return
	}
	st, _, ok := s.authorized(w, r)
	if !ok {
		return
	}
	if !macEqual(q.Get("c"), s.csrf(st)) {
		uploadFail(w, http.StatusForbidden, "Reload the page and try again.")
		return
	}
	var fld *form.Field
	for i := range st.Fields {
		if st.Fields[i].Key == r.PathValue("key") && st.Fields[i].Type == form.File {
			fld = &st.Fields[i]
		}
	}
	if fld == nil || st.Multi {
		notFound(w)
		return
	}
	if st.Status != store.Pending {
		uploadFail(w, http.StatusGone, "This form is closed.")
		return
	}
	if ref := q.Get("rm"); ref != "" {
		if err := s.store.RemoveUpload(st.ID, fld.Key, ref); err != nil {
			uploadError(w, err)
			return
		}
		uploadJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/octet-stream" {
		uploadFail(w, http.StatusUnsupportedMediaType, "That upload didn't make sense — try again.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxChunk))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			uploadFail(w, http.StatusRequestEntityTooLarge, "That piece of the file is too large.")
			return
		}
		uploadFail(w, http.StatusBadRequest, "The upload was interrupted — try again.")
		return
	}
	idx, e1 := strconv.Atoi(q.Get("i"))
	total, e2 := strconv.Atoi(q.Get("n"))
	if e1 != nil || e2 != nil {
		uploadFail(w, http.StatusBadRequest, "That upload didn't make sense — try again.")
		return
	}
	info, ref, err := s.store.PutChunk(store.Chunk{
		Form: st.ID, Key: fld.Key, Session: q.Get("u"), Index: idx, Total: total, Name: q.Get("name"), Data: body,
	})
	if err != nil {
		uploadError(w, err)
		return
	}
	if info == nil {
		uploadJSON(w, http.StatusOK, map[string]any{"received": idx + 1, "total": total})
		return
	}
	uploadJSON(w, http.StatusOK, map[string]any{
		"done": true, "ref": ref, "name": info.Name, "size": info.Size, "contentType": info.ContentType,
	})
}

func uploadError(w http.ResponseWriter, err error) {
	var ue *store.UploadError
	if !errors.As(err, &ue) {
		uploadFail(w, http.StatusInternalServerError, "Something went wrong saving that file — try again.")
		return
	}
	code := map[store.UploadKind]int{
		store.UploadBad: http.StatusBadRequest, store.UploadClosed: http.StatusGone,
		store.UploadTooBig: http.StatusRequestEntityTooLarge, store.UploadType: http.StatusUnsupportedMediaType,
	}[ue.Kind]
	uploadFail(w, code, ue.Msg)
}

func uploadFail(w http.ResponseWriter, code int, msg string) {
	uploadJSON(w, code, map[string]any{"error": msg})
}

// uploadJSON answers an upload call. The body is JSON for the page's script;
// the sandbox keeps it inert if it is ever opened on its own.
func uploadJSON(w http.ResponseWriter, code int, v any) {
	h := w.Header()
	baseHeaders(h)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
