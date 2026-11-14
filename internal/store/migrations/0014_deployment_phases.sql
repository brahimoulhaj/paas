-- The deploy state machine. While a deployment runs, phase is the step it is in
-- (pulling or building, starting, health_checking, switching, draining); once it ends,
-- the step it ended in. phases is a JSON array of {"phase", "at"}, one entry per step
-- entered and a last one named after the final status, for the timeline: a step lasts
-- until the next entry.
ALTER TABLE deployments ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN phases TEXT NOT NULL DEFAULT '[]';
