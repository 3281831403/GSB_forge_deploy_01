-- Create "build_jobs" table
CREATE TABLE "build_jobs" (
  "id" bigserial NOT NULL,
  "project_id" bigint NOT NULL,
  "revision_id" bigint NOT NULL,
  "user_id" bigint NOT NULL,
  "cloud_project_id" character varying(120) NOT NULL,
  "cloud_build_id" character varying(120) NULL,
  "cloud_status" character varying(30) NULL,
  "artifact_digest" character varying(120) NULL,
  "status" character varying(20) NOT NULL,
  "error" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_build_jobs_project_id" to table: "build_jobs"
CREATE INDEX "idx_build_jobs_project_id" ON "build_jobs" ("project_id");
-- Create index "idx_build_jobs_status" to table: "build_jobs"
CREATE INDEX "idx_build_jobs_status" ON "build_jobs" ("status");
-- Create index "idx_build_jobs_user_id" to table: "build_jobs"
CREATE INDEX "idx_build_jobs_user_id" ON "build_jobs" ("user_id");
