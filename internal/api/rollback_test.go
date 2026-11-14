package api

import "testing"

func TestRollbackAPI(t *testing.T) {
	e := newEnv(t)
	e.signIn()
	pid := e.must(201, "POST", "/api/projects", map[string]any{"name": "Shop"})["id"].(string)
	id := e.must(201, "POST", "/api/projects/"+pid+"/apps", map[string]any{"name": "web", "image": "nginx:1", "port": 80})["id"].(string)
	first := e.must(202, "POST", "/api/apps/"+id+"/deploy", nil)["id"].(string)
	e.deployer.Wait()
	e.must(200, "PATCH", "/api/apps/"+id, map[string]any{"image": "nginx:2"})
	second := e.must(202, "POST", "/api/apps/"+id+"/deploy", nil)["id"].(string)
	e.deployer.Wait()

	can := map[string]bool{}
	for _, d := range e.list("/api/apps/" + id + "/deployments") {
		can[d["id"].(string)] = d["can_roll_back"] == true
	}
	if !can[first] || can[second] {
		t.Fatalf("can_roll_back = %v, want only the first", can)
	}
	if code, m := e.call("POST", "/api/deployments/"+second+"/rollback", nil); code != 409 {
		t.Errorf("rollback to the live one = %d %v", code, m)
	}
	if code, _ := e.call("POST", "/api/deployments/nope/rollback", nil); code != 404 {
		t.Errorf("unknown = %d", code)
	}
	dep := e.must(202, "POST", "/api/deployments/"+first+"/rollback", nil)
	if dep["trigger"] != "rollback" || dep["rollback_of"] != first || dep["image"] != "nginx:1" {
		t.Fatalf("rollback = %v", dep)
	}
	e.deployer.Wait()
	a := e.must(200, "GET", "/api/apps/"+id, nil)
	if a["current_deployment_id"] != dep["id"] || a["image"] != "nginx:1" {
		t.Errorf("app after rollback = %v", a)
	}
}
