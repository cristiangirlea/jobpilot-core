-- An aggregator's posting link leads to the aggregator's own page. Some hand
-- over to the employer through a redirect (Arbeitnow's "<posting>/apply"), and
-- employer_url is where that redirect goes: the employer's own application
-- page, where the form is read and where the user applies. Empty until found.
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS employer_url text NOT NULL DEFAULT '';
