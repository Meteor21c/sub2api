ALTER TABLE model_test_plans
    ADD COLUMN interval_minutes INTEGER NOT NULL DEFAULT 120
    CHECK (interval_minutes BETWEEN 15 AND 1440);
