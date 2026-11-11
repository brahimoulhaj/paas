package store

import (
	"context"
	"reflect"
	"testing"
)

func TestMountPersistenceAndValidation(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, err := s.CreateProject(ctx, "mounts", "")
	if err != nil {
		t.Fatal(err)
	}
	in := AppInput{Name: "web", Image: "nginx", Port: 80, Mounts: []AppMount{{Type: "file", Target: "/etc/app.conf", Content: "hello\n", ReadOnly: true}}}
	a, err := s.CreateApp(ctx, p.ID, in)
	if err != nil || !reflect.DeepEqual(a.Mounts, in.Mounts) {
		t.Fatalf("mount round trip: %+v, %v", a.Mounts, err)
	}
	in.Mounts = []AppMount{}
	a, err = s.UpdateApp(ctx, a.ID, in)
	if err != nil || len(a.Mounts) != 0 {
		t.Fatal("clear mounts failed", err)
	}
	for _, mounts := range [][]AppMount{
		{{Type: "bind", Source: "relative", Target: "/data"}},
		{{Type: "volume", Source: "../bad", Target: "/data"}},
		{{Type: "file", Target: "/"}},
		{{Type: "file", Target: "/data"}, {Type: "file", Target: "/data"}},
		{{Type: "file", Target: "/a/../data"}},
	} {
		if ValidateAppMounts(mounts) == nil {
			t.Fatalf("accepted invalid mounts: %+v", mounts)
		}
	}
}
