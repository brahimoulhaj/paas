// Package executor runs workloads. The Executor interface is the seam for multiple servers
// later: everything above it (deploys, proxy, databases) talks to this interface and never
// to Docker directly. Today the only implementation is the local Docker daemon.
package executor

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when a container or image does not exist.
var ErrNotFound = errors.New("executor: not found")

// Kind says what a managed container is for.
type Kind string

const (
	KindApp      Kind = "app"
	KindProxy    Kind = "proxy"
	KindDatabase Kind = "database"
	KindService  Kind = "service" // built-in services: storage, registry, updater
)

// Executor is everything the panel needs from a container host.
type Executor interface {
	// Ping checks that the container host answers.
	Ping(ctx context.Context) error
	// EnsureNetwork creates the shared network when it does not exist yet.
	EnsureNetwork(ctx context.Context) error
	// PullImage pulls ref; onProgress may be nil.
	PullImage(ctx context.Context, ref string, onProgress func(PullProgress)) error
	// CreateContainer creates (but does not start) a container and returns its id.
	CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
	StartContainer(ctx context.Context, id string) error
	// StopContainer sends the stop signal and kills the container after grace.
	StopContainer(ctx context.Context, id string, grace time.Duration) error
	// RemoveContainer removes a container; force also kills a running one.
	// Removing a container that is already gone is not an error.
	RemoveContainer(ctx context.Context, id string, force bool) error
	InspectContainer(ctx context.Context, id string) (Container, error)
	// ListContainers returns every container managed by this panel, running or not.
	ListContainers(ctx context.Context) ([]Container, error)
	// Logs calls fn for each log line until the stream ends or ctx is cancelled.
	// With Follow it blocks for as long as the container runs.
	Logs(ctx context.Context, id string, opts LogOptions, fn func(LogLine)) error
	Close() error
}

// ContainerSpec describes a container to create. Kind and ResourceID become labels,
// which is how the panel recognises its own containers after a restart.
type ContainerSpec struct {
	Name       string // container name, also its DNS name on the shared network
	Image      string
	Kind       Kind
	ResourceID string            // id of the app, database or service this container belongs to
	Labels     map[string]string // extra labels, e.g. the deployment id
	Env        map[string]string
	Command    []string // overrides the image command when set
	Ports      []PortBinding
	Mounts     []Mount
	ExtraHosts []string // extra /etc/hosts entries, "name:ip"; "host-gateway" is the Docker host
	CPUs       float64  // CPU limit in cores, 0 = unlimited
	MemoryMB   int64    // memory limit in MiB, 0 = unlimited
	Restart    RestartPolicy
}

// PortBinding publishes a container port on the host. Apps do not need this: the proxy
// reaches them over the shared network. It is for the proxy itself and opt-in database ports.
type PortBinding struct {
	HostIP        string // empty = all interfaces
	HostPort      int
	ContainerPort int
	Protocol      string // "tcp" (default) or "udp"
}

// Mount attaches a host path, or a folder of a named Docker volume.
type Mount struct {
	Source   string // absolute host path; empty when Volume is set
	Volume   string // named volume; Source must be empty
	SubPath  string // with Volume: a folder inside the volume (it must exist) instead of its root
	Target   string
	ReadOnly bool
}

type RestartPolicy string

const (
	RestartUnlessStopped RestartPolicy = "unless-stopped" // default
	RestartNo            RestartPolicy = "no"
	RestartOnFailure     RestartPolicy = "on-failure"
	RestartAlways        RestartPolicy = "always"
)

// Container is the observed state of a managed container.
type Container struct {
	ID         string
	Name       string
	Image      string
	Kind       Kind
	ResourceID string
	Labels     map[string]string
	State      string // created, running, paused, restarting, removing, exited, dead
	Running    bool
	ExitCode   int
	Restarts   int // times Docker restarted it under its restart policy
	OOMKilled  bool
	Health     string // "", starting, healthy, unhealthy
	IP         string // address on the shared network, empty when not running
	CreatedAt  time.Time
	StartedAt  time.Time
}

// PullProgress is one progress update for one image layer.
type PullProgress struct {
	Layer   string // layer id, empty for whole-image messages
	Status  string // e.g. "Downloading", "Pull complete"
	Current int64
	Total   int64
}

type LogOptions struct {
	Follow bool
	Tail   int       // last N lines; 0 = everything
	Since  time.Time // zero = from the start
}

type LogLine struct {
	Stream string // "stdout" or "stderr"
	Time   time.Time
	Text   string
}
