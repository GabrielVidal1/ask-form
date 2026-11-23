package update

import "testing"

func TestRepoRejectsCleartextNonLoopback(t *testing.T) {
	t.Setenv("ASK_FORM_UPDATE_URL", "http://evil.example/repo")
	if got := Repo(); got != DefaultRepo {
		t.Fatalf("cleartext non-loopback accepted: %q", got)
	}
	t.Setenv("ASK_FORM_UPDATE_URL", "http://127.0.0.1:9999/repo/")
	if got := Repo(); got != "http://127.0.0.1:9999/repo" {
		t.Fatalf("loopback test server rejected: %q", got)
	}
	t.Setenv("ASK_FORM_UPDATE_URL", "https://mirror.example.com/ask-form/")
	if got := Repo(); got != "https://mirror.example.com/ask-form" {
		t.Fatalf("https mirror rejected: %q", got)
	}
}

func TestDueRefusesPrereleasesAndParseValidatesPre(t *testing.T) {
	if ok, _ := Due("v0.19.0", "v0.20.0-rc1", true); ok {
		t.Fatal("auto-upgrade must never install a pre-release")
	}
	if ok, _ := Due("v0.19.0", "v0.20.0-alpha.1", false); ok {
		t.Fatal("minor auto-upgrade must never install a pre-release")
	}
	if _, ok := Parse("v1.2.3-bad/path"); ok {
		t.Fatal("Parse accepted slash in pre-release")
	}
	if _, ok := Parse("v1.2.3-bad space"); ok {
		t.Fatal("Parse accepted space in pre-release")
	}
	if v, ok := Parse("v1.2.3-rc.1"); !ok || v.Pre != "rc.1" {
		t.Fatalf("Parse rejected valid pre-release: %+v %v", v, ok)
	}
}
