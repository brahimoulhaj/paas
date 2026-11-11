package deploy

import (
	"fmt"
	"os"
	"panel/internal/executor"
	"panel/internal/store"
	"path/filepath"
)

func (s *Service) prepareMounts(app store.App, dep store.Deployment) ([]executor.Mount, error) {
	if err := store.ValidateAppMounts(app.Mounts); err != nil {
		return nil, err
	}
	mounts := make([]executor.Mount, 0, len(app.Mounts))
	for i, m := range app.Mounts {
		mount := executor.Mount{Target: m.Target, ReadOnly: m.ReadOnly}
		switch m.Type {
		case "bind":
			mount.Source = m.Source
		case "volume":
			mount.Volume = m.Source
		case "file":
			// Each deployment gets immutable content so editing a file never changes the live container.
			relative := filepath.Join("deployments", app.ID, "files", dep.ID, fmt.Sprintf("%d", i))
			filename := filepath.Join(s.opts.DataDir, relative)
			if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filename, []byte(m.Content), 0644); err != nil {
				return nil, err
			}
			if s.opts.DataVolume != "" {
				mount.Volume, mount.SubPath, mount.VolumeFile = s.opts.DataVolume, relative, true
			} else {
				absolute, err := filepath.Abs(filename)
				if err != nil {
					return nil, err
				}
				mount.Source = absolute
			}
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}
