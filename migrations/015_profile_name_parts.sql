-- Application forms ask for a first and a last name, and which word of the one
-- stored name is which cannot be told from the name itself: "Pirvan Octavian"
-- is surname first. Until these are filled, a name split from the full name is
-- offered for review rather than sent as a fact.
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS given_name text NOT NULL DEFAULT '';
ALTER TABLE user_profiles ADD COLUMN IF NOT EXISTS family_name text NOT NULL DEFAULT '';
