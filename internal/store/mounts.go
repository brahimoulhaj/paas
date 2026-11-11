package store

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var volumeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func ValidateAppMounts(mounts []AppMount) error {
	if len(mounts) > 50 {
		return fmt.Errorf("at most 50 mounts are allowed")
	}
	targets := map[string]bool{}
	total := 0
	for _, m := range mounts {
		if !path.IsAbs(m.Target) || path.Clean(m.Target) != m.Target || m.Target == "/" || strings.ContainsAny(m.Target, "\x00\r\n:") || targets[m.Target] {
			return fmt.Errorf("mount targets must be unique, absolute container paths other than /")
		}
		targets[m.Target] = true
		switch m.Type {
		case "bind":
			if !path.IsAbs(m.Source) || path.Clean(m.Source) != m.Source || strings.ContainsAny(m.Source, "\x00\r\n:") {
				return fmt.Errorf("bind source must be an absolute Docker host path")
			}
		case "volume":
			if !volumeName.MatchString(m.Source) || len(m.Source) > 128 {
				return fmt.Errorf("volume name must contain letters, numbers, dots, underscores or dashes")
			}
		case "file":
			if m.Source != "" {
				return fmt.Errorf("file mounts cannot specify a source")
			}
			total += len(m.Content)
		default:
			return fmt.Errorf("mount type must be bind, volume or file")
		}
		if m.Type != "file" && m.Content != "" {
			return fmt.Errorf("only file mounts can contain file content")
		}
	}
	if total > 128*1024 {
		return fmt.Errorf("mounted file contents must total at most 128 KiB")
	}
	return nil
}
