package store

import (
	"context"
	"errors"
	"testing"
)

func TestProjectsAppsDomains(t *testing.T) {
	s, ctx := open(t), context.Background()

	p, err := s.CreateProject(ctx, "Shop", "the shop")
	if err != nil || p.ID == "" || p.AppCount != 0 || p.CreatedAt.IsZero() {
		t.Fatalf("create project = %+v, %v", p, err)
	}
	if _, err := s.CreateProject(ctx, "shop", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate project name (case-insensitive) = %v", err)
	}
	if _, err := s.GetProject(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project = %v", err)
	}

	in := AppInput{Name: "web", Image: "nginx:1.27", Port: 80, CPULimit: 0.5, MemoryMB: 128}
	a, err := s.CreateApp(ctx, p.ID, in)
	if err != nil || a.SourceType != "image" || a.DesiredState != StateStopped || a.CurrentDeploymentID != "" || a.CPULimit != 0.5 {
		t.Fatalf("create app = %+v, %v", a, err)
	}
	if _, err := s.CreateApp(ctx, p.ID, in); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate app name in project = %v", err)
	}
	if _, err := s.CreateApp(ctx, "missing", in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("app in missing project = %v", err)
	}
	p2, _ := s.CreateProject(ctx, "Blog", "")
	b, err := s.CreateApp(ctx, p2.ID, in) // same name, other project
	if err != nil {
		t.Fatalf("same app name in another project: %v", err)
	}
	if ok, _ := s.AppExists(ctx, a.ID); !ok {
		t.Error("AppExists = false")
	}
	if got, _ := s.GetProject(ctx, p.ID); got.AppCount != 1 {
		t.Errorf("app count = %d", got.AppCount)
	}
	if list, _ := s.ListProjects(ctx); len(list) != 2 || list[0].Name != "Blog" {
		t.Errorf("projects = %+v", list)
	}

	in.Image, in.Port = "nginx:1.28", 8080
	if a, err = s.UpdateApp(ctx, a.ID, in); err != nil || a.Image != "nginx:1.28" || a.Port != 8080 {
		t.Fatalf("update app = %+v, %v", a, err)
	}

	// Domains: unique panel-wide.
	d, err := s.CreateDomain(ctx, a.ID, "shop.example.com", "")
	if err != nil || d.TLSMode != "auto" {
		t.Fatalf("create domain = %+v, %v", d, err)
	}
	if _, err := s.CreateDomain(ctx, b.ID, "shop.example.com", ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("hostname reuse across apps = %v", err)
	}
	if _, err := s.CreateDomain(ctx, a.ID, "www.shop.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.DomainExists(ctx, "shop.example.com"); !ok {
		t.Error("DomainExists = false")
	}

	// Routes: no upstream until a deployment is current and the app is running.
	routes, err := s.ProxyRoutes(ctx)
	if err != nil || len(routes) != 1 || len(routes[0].Hosts) != 2 || routes[0].Upstream != "" {
		t.Fatalf("routes before deploy = %+v, %v", routes, err)
	}
	dep, err := s.CreateDeployment(ctx, a.ID, "manual", a.Image, a.Port, func(id string) string { return "deployments/" + id + ".log" })
	if err != nil || dep.Status != DeployQueued || dep.LogPath != "deployments/"+dep.ID+".log" || !dep.StartedAt.IsZero() {
		t.Fatalf("create deployment = %+v, %v", dep, err)
	}
	if err := s.MarkDeploymentRunning(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentContainer(ctx, dep.ID, "cid", "app-web-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppCurrentDeployment(ctx, a.ID, dep.ID, StateRunning); err != nil {
		t.Fatal(err)
	}
	if routes, _ = s.ProxyRoutes(ctx); routes[0].Upstream != "app-web-1:8080" {
		t.Fatalf("route upstream = %q", routes[0].Upstream)
	}
	// The route keeps dialling the deployed port even after the setting changes.
	in.Port = 9090
	if _, err := s.UpdateApp(ctx, a.ID, in); err != nil {
		t.Fatal(err)
	}
	if routes, _ = s.ProxyRoutes(ctx); routes[0].Upstream != "app-web-1:8080" {
		t.Fatalf("route followed an undeployed port change: %q", routes[0].Upstream)
	}
	if err := s.SetAppDesiredState(ctx, a.ID, StateStopped); err != nil {
		t.Fatal(err)
	}
	if routes, _ = s.ProxyRoutes(ctx); routes[0].Upstream != "" {
		t.Fatalf("stopped app still has upstream %q", routes[0].Upstream)
	}

	if err := s.FinishDeployment(ctx, dep.ID, DeploySucceeded, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetDeployment(ctx, dep.ID)
	if got.Status != DeploySucceeded || got.StartedAt.IsZero() || got.FinishedAt.IsZero() || got.Port != 8080 {
		t.Fatalf("finished deployment = %+v", got)
	}

	// Unfinished attempts are listed for a restart and can be queued again, counting starts;
	// finished ones are left alone.
	open1, _ := s.CreateDeployment(ctx, a.ID, "manual", a.Image, a.Port, func(string) string { return "" })
	if err := s.MarkDeploymentRunning(ctx, open1.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentContainer(ctx, open1.ID, "c1", "app-web-2"); err != nil {
		t.Fatal(err)
	}
	if list, err := s.ListUnfinishedDeployments(ctx); err != nil || len(list) != 1 || list[0].ID != open1.ID || list[0].Attempts != 1 {
		t.Fatalf("unfinished = %+v, %v", list, err)
	}
	if err := s.RequeueDeployment(ctx, open1.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDeployment(ctx, open1.ID); got.Status != DeployQueued || got.ContainerName != "" || got.Attempts != 1 {
		t.Fatalf("requeued deployment = %+v", got)
	}
	if err := s.RequeueDeployment(ctx, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("requeue of a finished deployment = %v", err)
	}
	if list, _ := s.ListDeployments(ctx, a.ID, 10); len(list) != 2 || list[0].ID != open1.ID {
		t.Fatalf("deployments newest first = %+v", list)
	}

	// Deleting: a project with apps is refused; an app takes its domains and deployments along.
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("delete project with apps = %v", err)
	}
	if err := s.DeleteApp(ctx, a.ID); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if _, err := s.GetDeployment(ctx, dep.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deployment survived its app: %v", err)
	}
	if ok, _ := s.DomainExists(ctx, "shop.example.com"); ok {
		t.Error("domain survived its app")
	}
	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatalf("delete empty project: %v", err)
	}
	if err := s.DeleteProject(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete project twice = %v", err)
	}
}

func TestEvents(t *testing.T) {
	s, ctx := open(t), context.Background()
	u, _ := s.CreateUser(ctx, "owner@example.com", "hash", "owner")
	for _, e := range []Event{
		{UserID: u.ID, Actor: u.Email, Action: "project.create", TargetType: "project", TargetID: "p1", TargetName: "Shop"},
		{Actor: "system", Action: "deployment.succeeded", TargetType: "app", TargetID: "a1", TargetName: "web"},
	} {
		if err := s.AddEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListEvents(ctx, 10)
	if err != nil || len(list) != 2 {
		t.Fatalf("events = %+v, %v", list, err)
	}
	var system, user Event
	for _, e := range list {
		if e.Actor == "system" {
			system = e
		} else {
			user = e
		}
	}
	if system.UserID != "" || user.UserID != u.ID || user.TargetName != "Shop" || user.CreatedAt.IsZero() {
		t.Fatalf("events = %+v", list)
	}
	if list, _ := s.ListEvents(ctx, 1); len(list) != 1 {
		t.Fatalf("limit ignored: %d", len(list))
	}
}

func TestAppEnvironmentRoundTrip(t *testing.T) {
	s, ctx := open(t), context.Background()
	p, err := s.CreateProject(ctx, "Environment", "")
	if err != nil {
		t.Fatal(err)
	}
	in := AppInput{Name: "web", Image: "nginx:1", Port: 80, Environment: map[string]string{"TOKEN": "secret=with#symbols", "EMPTY": "", "MULTILINE": "one\ntwo"}}
	a, err := s.CreateApp(ctx, p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.GetApp(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range in.Environment {
		if a.Environment[k] != v {
			t.Fatalf("variable %s did not round-trip", k)
		}
	}
	in.Environment = map[string]string{"NEW": "value"}
	a, err = s.UpdateApp(ctx, a.ID, in)
	if err != nil || len(a.Environment) != 1 || a.Environment["NEW"] != "value" {
		t.Fatalf("replace: %v", err)
	}
	in.Environment = map[string]string{}
	a, err = s.UpdateApp(ctx, a.ID, in)
	if err != nil || len(a.Environment) != 0 {
		t.Fatalf("clear: %v", err)
	}
}
