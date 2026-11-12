package executor

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// Terminal is an attached container PTY, never a shell on the panel host.
type Terminal interface {
	io.ReadWriteCloser
	Resize(context.Context, uint, uint) error
}

type TerminalExecutor interface {
	OpenTerminal(context.Context, string, string, uint, uint) (Terminal, error)
}

func (d *Docker) OpenTerminal(ctx context.Context, containerID, shell string, cols, rows uint) (Terminal, error) {
	if shell != "/bin/sh" && shell != "/bin/bash" && shell != "/bin/ash" {
		return nil, errors.New("unsupported shell")
	}
	created, err := d.cli.ExecCreate(ctx, containerID, client.ExecCreateOptions{
		Cmd: []string{shell, "-i"}, TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true,
		Env: []string{"TERM=xterm-256color"}, ConsoleSize: client.ConsoleSize{Width: cols, Height: rows},
	})
	if err != nil {
		return nil, err
	}
	attached, err := d.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{TTY: true, ConsoleSize: client.ConsoleSize{Width: cols, Height: rows}})
	if err != nil {
		return nil, err
	}
	return &dockerTerminal{docker: d, id: created.ID, attached: attached}, nil
}

type dockerTerminal struct {
	docker   *Docker
	id       string
	attached client.ExecAttachResult
	once     sync.Once
}

func (t *dockerTerminal) Read(p []byte) (int, error) { return t.attached.Reader.Read(p) }
func (t *dockerTerminal) Write(p []byte) (int, error) {
	t.attached.Conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return t.attached.Conn.Write(p)
}
func (t *dockerTerminal) Close() error { t.once.Do(func() { t.attached.Close() }); return nil }
func (t *dockerTerminal) Resize(ctx context.Context, cols, rows uint) error {
	_, err := t.docker.cli.ExecResize(ctx, t.id, client.ExecResizeOptions{Width: cols, Height: rows})
	return err
}
