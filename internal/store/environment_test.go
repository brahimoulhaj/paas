package store

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"panel/internal/secret"
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

func TestEnvironmentEncryptedAtRest(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, err := s.CreateProject(ctx, "environment-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	// Written before the key is set, as by a panel from before encryption.
	legacy, err := s.CreateApp(ctx, p.ID, AppInput{Name: "old", Image: "nginx:1", Port: 80, Environment: map[string]string{"DB_PASSWORD": "hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	stored := func(appID string) string {
		var env string
		if err := s.db.QueryRowContext(ctx, `SELECT environment FROM apps WHERE id = ?`, appID).Scan(&env); err != nil {
			t.Fatal(err)
		}
		return env
	}
	if !strings.Contains(stored(legacy.ID), "hunter2") {
		t.Fatalf("without a key the environment is plain JSON: %q", stored(legacy.ID))
	}

	box, err := secret.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetApp(ctx, legacy.ID)
	n, err := s.UseSecrets(ctx, box)
	if err != nil || n != 1 {
		t.Fatalf("UseSecrets = %d, %v; want 1 app encrypted", n, err)
	}
	if env := stored(legacy.ID); !secret.IsSealed(env) || strings.Contains(env, "hunter2") {
		t.Fatalf("legacy environment not encrypted: %q", env)
	}
	after, err := s.GetApp(ctx, legacy.ID)
	if err != nil || after.Environment["DB_PASSWORD"] != "hunter2" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("after encryption: %+v, %v", after, err)
	}
	if n, err := s.UseSecrets(ctx, box); err != nil || n != 0 {
		t.Fatalf("second UseSecrets = %d, %v; want nothing left to encrypt", n, err)
	}

	fresh, err := s.CreateApp(ctx, p.ID, AppInput{Name: "new", Image: "nginx:1", Port: 80, Environment: map[string]string{"API_KEY": "s3cr3t"}})
	if err != nil {
		t.Fatal(err)
	}
	if env := stored(fresh.ID); !secret.IsSealed(env) || strings.Contains(env, "s3cr3t") {
		t.Fatalf("new environment stored in clear: %q", env)
	}
	if fresh.Environment["API_KEY"] != "s3cr3t" {
		t.Fatalf("new environment = %v", fresh.Environment)
	}
	if _, err := s.UpdateApp(ctx, fresh.ID, AppInput{Name: "new", Image: "nginx:1", Port: 80, Environment: map[string]string{"API_KEY": "rotated"}}); err != nil {
		t.Fatal(err)
	}
	if env := stored(fresh.ID); strings.Contains(env, "rotated") {
		t.Fatalf("updated environment stored in clear: %q", env)
	}
	if apps, err := s.ListApps(ctx, p.ID); err != nil || len(apps) != 2 {
		t.Fatalf("ListApps = %+v, %v", apps, err)
	}

	// Another key cannot read it, and says which app is affected.
	other, _ := secret.New(bytes.Repeat([]byte{9}, 32))
	s.box = other
	if _, err := s.GetApp(ctx, fresh.ID); err == nil || !strings.Contains(err.Error(), fresh.ID) {
		t.Fatalf("read with the wrong key: %v", err)
	}
}
