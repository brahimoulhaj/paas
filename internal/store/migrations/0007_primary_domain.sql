ALTER TABLE domains ADD COLUMN is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1));

-- Existing apps use their first-added domain, with the sortable ID breaking timestamp ties.
UPDATE domains SET is_primary = 1
WHERE id = (
    SELECT first.id FROM domains AS first
    WHERE first.app_id = domains.app_id
    ORDER BY first.created_at, first.id LIMIT 1
);

CREATE UNIQUE INDEX domains_one_primary ON domains(app_id) WHERE is_primary = 1;
