CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_profiles (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL DEFAULT '', email text NOT NULL DEFAULT '', phone text NOT NULL DEFAULT '',
    location text NOT NULL DEFAULT '', linkedin text NOT NULL DEFAULT '', github text NOT NULL DEFAULT '', portfolio text NOT NULL DEFAULT '',
    current_title text NOT NULL DEFAULT '', desired_titles text[] NOT NULL DEFAULT '{}', seniority text NOT NULL DEFAULT '',
    years_experience numeric NOT NULL DEFAULT 0, certifications text[] NOT NULL DEFAULT '{}', languages text[] NOT NULL DEFAULT '{}',
    salary_expectation numeric NOT NULL DEFAULT 0, rate_expectation numeric NOT NULL DEFAULT 0, currency text NOT NULL DEFAULT 'EUR',
    work_preference text[] NOT NULL DEFAULT '{}', preferred_countries text[] NOT NULL DEFAULT '{}', preferred_timezones text[] NOT NULL DEFAULT '{}',
    work_authorization text NOT NULL DEFAULT '', notice_period text NOT NULL DEFAULT '', industry_preferences text[] NOT NULL DEFAULT '{}',
    summary text NOT NULL DEFAULT '', updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS companies (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), name text NOT NULL, normalized_name text NOT NULL,
    domain text NOT NULL DEFAULT '', aliases text[] NOT NULL DEFAULT '{}', UNIQUE(normalized_name, domain)
);

CREATE TABLE IF NOT EXISTS work_experiences (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    company text NOT NULL, company_domain text NOT NULL DEFAULT '', title text NOT NULL,
    start_date text NOT NULL DEFAULT '', end_date text NOT NULL DEFAULT '', current boolean NOT NULL DEFAULT false,
    description text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS work_experiences_user_idx ON work_experiences(user_id);

CREATE TABLE IF NOT EXISTS skills (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL, level text NOT NULL DEFAULT '', years numeric NOT NULL DEFAULT 0,
    UNIQUE(user_id, name)
);

CREATE TABLE IF NOT EXISTS projects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL, description text NOT NULL DEFAULT '', url text NOT NULL DEFAULT '', skills text[] NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS company_blacklist (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    company_name text NOT NULL, normalized_name text NOT NULL, company_domain text NOT NULL DEFAULT '', reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(user_id, normalized_name, company_domain)
);

CREATE TABLE IF NOT EXISTS filter_presets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL, active boolean NOT NULL DEFAULT true, included_titles text[] NOT NULL DEFAULT '{}',
    excluded_title_words text[] NOT NULL DEFAULT '{}', locations text[] NOT NULL DEFAULT '{}', remote_types text[] NOT NULL DEFAULT '{}',
    min_salary numeric NOT NULL DEFAULT 0, currency text NOT NULL DEFAULT 'EUR', employment_types text[] NOT NULL DEFAULT '{}',
    excluded_industries text[] NOT NULL DEFAULT '{}', required_skills text[] NOT NULL DEFAULT '{}', max_age_days integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS jobs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    company_id uuid REFERENCES companies(id), title text NOT NULL, company text NOT NULL, company_normalized text NOT NULL,
    company_domain text NOT NULL DEFAULT '', location text NOT NULL DEFAULT '', remote_type text NOT NULL DEFAULT '',
    salary_min numeric NOT NULL DEFAULT 0, salary_max numeric NOT NULL DEFAULT 0, currency text NOT NULL DEFAULT 'EUR',
    employment_type text NOT NULL DEFAULT '', industry text NOT NULL DEFAULT '', description text NOT NULL,
    apply_url text NOT NULL DEFAULT '', posted_at timestamptz, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS jobs_user_created_idx ON jobs(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS job_matches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, filter_id uuid REFERENCES filter_presets(id) ON DELETE SET NULL,
    rejected boolean NOT NULL, score integer NOT NULL, rejection_reasons jsonb NOT NULL DEFAULT '[]', warnings jsonb NOT NULL DEFAULT '[]',
    reasons jsonb NOT NULL DEFAULT '[]', matched_skills jsonb NOT NULL DEFAULT '[]', missing_skills jsonb NOT NULL DEFAULT '[]',
    red_flags jsonb NOT NULL DEFAULT '[]', breakdown jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, job_id)
);
CREATE INDEX IF NOT EXISTS job_matches_user_score_idx ON job_matches(user_id, rejected, score DESC);

CREATE TABLE IF NOT EXISTS applications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, status text NOT NULL DEFAULT 'draft', notes text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(user_id, job_id),
    CHECK (status IN ('draft','ready','applied','interview','rejected','offer','withdrawn'))
);

CREATE TABLE IF NOT EXISTS application_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    application_id uuid REFERENCES applications(id) ON DELETE CASCADE, job_id uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    document_type text NOT NULL, content text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(user_id, job_id, document_type)
);

CREATE TABLE IF NOT EXISTS application_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE, event_type text NOT NULL,
    from_status text NOT NULL DEFAULT '', to_status text NOT NULL DEFAULT '', metadata jsonb NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL DEFAULT now()
);

