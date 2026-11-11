package store

import (
	"context"
	"reflect"
	"testing"
)

func TestEnvironmentSpecialValuesRoundTrip(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, err := s.CreateProject(ctx, "environment-test", "")
	if err != nil {
		t.Fatal(err)
	}
	in := AppInput{Name: "web", Image: "nginx:latest", Port: 80, Environment: map[string]string{"EMPTY": "", "TOKEN": "a=b#c", "MULTILINE": "first\nsecond", "UNICODE": "مرحبا"}}
	a, err := s.CreateApp(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Environment, in.Environment) {
		t.Fatalf("environment did not round-trip: %#v", a.Environment)
	}
	in.Environment = map[string]string{"REPLACEMENT": "new"}
	a, err = s.UpdateApp(ctx, a.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Environment, in.Environment) {
		t.Fatal("environment replacement failed")
	}
	in.Environment = map[string]string{}
	a, err = s.UpdateApp(ctx, a.ID, in)
	if err != nil || len(a.Environment) != 0 {
		t.Fatalf("clear environment: %v, %v", a.Environment, err)
	}
}
