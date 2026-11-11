package executor

import "context"

// Report is the result of comparing the container host with what the panel knows.
type Report struct {
	Adopted []Container // managed containers that belong to a known resource
	Orphans []Container // managed containers whose resource the panel no longer knows
}

// Reconcile runs at startup. Containers keep running while the panel is down or being
// updated, so the panel adopts what is already there instead of recreating it. Orphans
// are only reported: removing a container is a decision for the owner, not for startup.
//
// known reports whether a resource of that kind and id exists in the panel's database.
func Reconcile(ctx context.Context, ex Executor, known func(kind Kind, resourceID string) bool) (Report, error) {
	list, err := ex.ListContainers(ctx)
	if err != nil {
		return Report{}, err
	}
	var r Report
	for _, c := range list {
		if c.ResourceID != "" && known(c.Kind, c.ResourceID) {
			r.Adopted = append(r.Adopted, c)
		} else {
			r.Orphans = append(r.Orphans, c)
		}
	}
	return r, nil
}
