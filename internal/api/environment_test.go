package api

import (
	"net/http/httptest"
	"panel/internal/store"
	"testing"
)

func TestEnvironmentValidation(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"APP_ENV", "production", true}, {"EMPTY", "", true}, {"1INVALID", "v", false}, {"BAD=KEY", "v", false}, {"BAD", "a\x00b", false},
	} {
		in := store.AppInput{Name: "web", SourceType: "image", Image: "nginx:latest", Port: 80, Environment: map[string]string{tc.key: tc.value}}
		if validApp(httptest.NewRecorder(), in) != tc.valid {
			t.Errorf("unexpected validation for %q", tc.key)
		}
	}
	original := store.AppInput{Environment: map[string]string{"KEEP": "value"}}
	if (appFields{}).apply(original).Environment["KEEP"] != "value" {
		t.Fatal("omitted environment must be preserved")
	}
	empty := map[string]string{}
	if len((appFields{Environment: &empty}).apply(original).Environment) != 0 {
		t.Fatal("empty environment must clear variables")
	}
}
