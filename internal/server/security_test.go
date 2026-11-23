package server

import (
	"testing"

	"github.com/GabrielVidal1/ask-form/internal/form"
)

func TestVetChatFieldsBlocksCredentialTextareasTablesAndPasswordWidgets(t *testing.T) {
	cases := []struct {
		name   string
		fields []form.Field
	}{
		{
			name:   "textarea named api_key",
			fields: []form.Field{{Key: "api_key", Label: "API key", Type: form.Textarea}},
		},
		{
			name: "list column named password",
			fields: []form.Field{{
				Key: "creds", Label: "Accounts", Type: form.List,
				Cols: []form.Column{{Key: "password", Label: "Password", Type: form.Text}},
			}},
		},
		{
			name: "widget with password input",
			fields: []form.Field{{
				Key: "login", Label: "Sign in", Type: form.Widget,
				HTML: `<input type="password" id="p">`,
			}},
		},
	}
	for _, tc := range cases {
		if err := vetChatFields(tc.fields); err == nil {
			t.Errorf("%s: expected vetChatFields to refuse", tc.name)
		}
	}
}
