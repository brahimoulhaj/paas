package deploy

import (
	"os"
	"panel/internal/store"
	"testing"
)

func TestPrepareAppMounts(t *testing.T) {
	s := &Service{opts: Options{DataDir: t.TempDir()}}
	app := store.App{ID: "app", Mounts: []store.AppMount{
		{Type: "volume", Source: "app-data", Target: "/data"},
		{Type: "bind", Source: "/srv/uploads", Target: "/uploads", ReadOnly: true},
		{Type: "file", Target: "/etc/app.conf", Content: "first", ReadOnly: true},
	}}
	first, err := s.prepareMounts(app, store.Deployment{ID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if first[0].Volume != "app-data" || first[1].Source != "/srv/uploads" || !first[1].ReadOnly {
		t.Fatal("wrong mount mapping")
	}
	app.Mounts[2].Content = "second"
	second, err := s.prepareMounts(app, store.Deployment{ID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(first[2].Source)
	if err != nil || string(old) != "first" || first[2].Source == second[2].Source {
		t.Fatal("deployments must retain their own file contents")
	}
	s.opts.DataVolume = "panel-data"
	mounted, err := s.prepareMounts(app, store.Deployment{ID: "third"})
	if err != nil || !mounted[2].VolumeFile || mounted[2].Volume != "panel-data" || mounted[2].SubPath != "deployments/app/files/third/2" {
		t.Fatalf("volume mapping: %+v, %v", mounted, err)
	}
}
