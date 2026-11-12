package deploy

import (
	"context"
	"errors"
	"panel/internal/executor"
)

func (s *Service) OpenTerminal(ctx context.Context, appID, shell string, cols, rows uint) (executor.Terminal, error) {
	_, dep, err := s.current(ctx, appID)
	if err != nil {
		return nil, err
	}
	c, err := s.opts.Executor.InspectContainer(ctx, dep.ContainerName)
	if err != nil {
		return nil, err
	}
	if !c.Running {
		return nil, errors.New("start the container before opening a terminal")
	}
	ex, ok := s.opts.Executor.(executor.TerminalExecutor)
	if !ok {
		return nil, errors.New("container terminal is unavailable")
	}
	return ex.OpenTerminal(ctx, c.ID, shell, cols, rows)
}
