package main

import (
	"errors"
	"reflect"
	"testing"
)

func TestBrowserCmd(t *testing.T) {
	found := func(string) (string, error) { return "/usr/bin/xdg-open", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	for _, c := range []struct {
		name, goos string
		env        map[string]string
		look       func(string) (string, error)
		want       []string
	}{
		{"mac", "darwin", nil, missing, []string{"open"}},
		{"mac over ssh", "darwin", map[string]string{"SSH_CONNECTION": "1.2.3.4 22 5.6.7.8 22"}, missing, nil},
		{"linux x11", "linux", map[string]string{"DISPLAY": ":0"}, found, []string{"/usr/bin/xdg-open"}},
		{"linux wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, found, []string{"/usr/bin/xdg-open"}},
		{"linux headless", "linux", nil, found, nil},
		{"linux no xdg-open", "linux", map[string]string{"DISPLAY": ":0"}, missing, nil},
		{"linux over ssh -X", "linux", map[string]string{"DISPLAY": "localhost:10.0", "SSH_TTY": "/dev/pts/1"}, found, nil},
		{"windows", "windows", nil, missing, []string{"rundll32", "url.dll,FileProtocolHandler"}},
	} {
		got := browserCmd(c.goos, func(k string) string { return c.env[k] }, c.look)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
