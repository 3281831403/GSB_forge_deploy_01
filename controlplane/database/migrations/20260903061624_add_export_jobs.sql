-- Create "export_jobs" table
CREATE TABLE "export_jobs" (
  "id" bigserial NOT NULL,
  "project_id" bigint NOT NULL,
  "revision_id" bigint NOT NULL,
  "user_id" bigint NOT NULL,
  "repo_name" character varying(100) NOT NULL,
  "private" boolean NOT NULL,
  "status" character varying(20) NOT NULL,
  "repo_url" character varying(255) NULL,
  "error" text NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_export_jobs_project_id" to table: "export_jobs"
CREATE INDEX "idx_export_jobs_project_id" ON "export_jobs" ("project_id");
-- Create index "idx_export_jobs_status" to table: "export_jobs"
CREATE INDEX "idx_export_jobs_status" ON "export_jobs" ("status");
-- Create index "idx_export_jobs_user_id" to table: "export_jobs"
CREATE INDEX "idx_export_jobs_user_id" ON "export_jobs" ("user_id");
