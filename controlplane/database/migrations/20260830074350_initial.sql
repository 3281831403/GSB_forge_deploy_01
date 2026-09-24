-- Create "audit_events" table
CREATE TABLE "audit_events" (
  "id" bigserial NOT NULL,
  "organization_id" bigint NULL,
  "actor_user_id" bigint NULL,
  "action" character varying(60) NOT NULL,
  "target_type" character varying(60) NULL,
  "target_id" character varying(120) NULL,
  "created_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_audit_events_action" to table: "audit_events"
CREATE INDEX "idx_audit_events_action" ON "audit_events" ("action");
-- Create index "idx_audit_events_actor_user_id" to table: "audit_events"
CREATE INDEX "idx_audit_events_actor_user_id" ON "audit_events" ("actor_user_id");
-- Create index "idx_audit_events_created_at" to table: "audit_events"
CREATE INDEX "idx_audit_events_created_at" ON "audit_events" ("created_at");
-- Create index "idx_audit_events_organization_id" to table: "audit_events"
CREATE INDEX "idx_audit_events_organization_id" ON "audit_events" ("organization_id");
-- Create "auth_group_permissions" table
CREATE TABLE "auth_group_permissions" (
  "group_id" bigint NOT NULL,
  "permission_id" bigint NOT NULL,
  PRIMARY KEY ("group_id", "permission_id")
);
-- Create "auth_user_groups" table
CREATE TABLE "auth_user_groups" (
  "user_id" bigint NOT NULL,
  "group_id" bigint NOT NULL,
  PRIMARY KEY ("user_id", "group_id")
);
-- Create "auth_user_permissions" table
CREATE TABLE "auth_user_permissions" (
  "user_id" bigint NOT NULL,
  "permission_id" bigint NOT NULL,
  PRIMARY KEY ("user_id", "permission_id")
);
-- Create "groups" table
CREATE TABLE "groups" (
  "id" bigserial NOT NULL,
  "name" character varying(100) NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_groups_name" to table: "groups"
CREATE UNIQUE INDEX "idx_groups_name" ON "groups" ("name");
-- Create "invitations" table
CREATE TABLE "invitations" (
  "id" bigserial NOT NULL,
  "organization_id" bigint NOT NULL,
  "email" character varying(255) NOT NULL,
  "role" character varying(20) NOT NULL,
  "token_hash" character varying(64) NOT NULL,
  "invited_by_user_id" bigint NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "accepted_at" timestamptz NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_invitations_accepted_at" to table: "invitations"
CREATE INDEX "idx_invitations_accepted_at" ON "invitations" ("accepted_at");
-- Create index "idx_invitations_email" to table: "invitations"
CREATE INDEX "idx_invitations_email" ON "invitations" ("email");
-- Create index "idx_invitations_token_hash" to table: "invitations"
CREATE UNIQUE INDEX "idx_invitations_token_hash" ON "invitations" ("token_hash");
-- Create index "uidx_pending_invite" to table: "invitations"
CREATE UNIQUE INDEX "uidx_pending_invite" ON "invitations" ("organization_id", "email") WHERE (accepted_at IS NULL);
-- Create "organization_members" table
CREATE TABLE "organization_members" (
  "id" bigserial NOT NULL,
  "organization_id" bigint NOT NULL,
  "user_id" bigint NOT NULL,
  "role" character varying(20) NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "uidx_org_member" to table: "organization_members"
CREATE UNIQUE INDEX "uidx_org_member" ON "organization_members" ("organization_id", "user_id");
-- Create "organizations" table
CREATE TABLE "organizations" (
  "id" bigserial NOT NULL,
  "name" character varying(120) NOT NULL,
  "slug" character varying(120) NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_organizations_created_at" to table: "organizations"
CREATE INDEX "idx_organizations_created_at" ON "organizations" ("created_at");
-- Create index "idx_organizations_slug" to table: "organizations"
CREATE UNIQUE INDEX "idx_organizations_slug" ON "organizations" ("slug");
-- Create "permissions" table
CREATE TABLE "permissions" (
  "id" bigserial NOT NULL,
  "permission_key" character varying(120) NOT NULL,
  "description" character varying(255) NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_permissions_key" to table: "permissions"
CREATE UNIQUE INDEX "idx_permissions_key" ON "permissions" ("permission_key");
-- Create "project_revisions" table
CREATE TABLE "project_revisions" (
  "id" bigserial NOT NULL,
  "project_id" bigint NOT NULL,
  "spec_version" bigint NOT NULL,
  "spec_json" text NOT NULL,
  "spec_hash" character varying(64) NOT NULL,
  "parent_revision_id" bigint NULL,
  "created_by" bigint NOT NULL,
  "created_at" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_project_revisions_parent" FOREIGN KEY ("parent_revision_id") REFERENCES "project_revisions" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_project_revisions_parent_revision_id" to table: "project_revisions"
CREATE INDEX "idx_project_revisions_parent_revision_id" ON "project_revisions" ("parent_revision_id");
-- Create index "idx_project_revisions_project_id" to table: "project_revisions"
CREATE INDEX "idx_project_revisions_project_id" ON "project_revisions" ("project_id");
-- Create index "idx_project_revisions_spec_hash" to table: "project_revisions"
CREATE INDEX "idx_project_revisions_spec_hash" ON "project_revisions" ("spec_hash");
-- Create "projects" table
CREATE TABLE "projects" (
  "id" bigserial NOT NULL,
  "organization_id" bigint NOT NULL,
  "name" character varying(120) NOT NULL,
  "slug" character varying(120) NOT NULL,
  "head_revision_id" bigint NULL,
  "cloud_project_id" character varying(64) NULL,
  "created_by" bigint NOT NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_projects_cloud_project_id" to table: "projects"
CREATE UNIQUE INDEX "idx_projects_cloud_project_id" ON "projects" ("cloud_project_id");
-- Create index "idx_projects_head_revision_id" to table: "projects"
CREATE INDEX "idx_projects_head_revision_id" ON "projects" ("head_revision_id");
-- Create index "uidx_org_project_slug" to table: "projects"
CREATE UNIQUE INDEX "uidx_org_project_slug" ON "projects" ("organization_id", "slug");
-- Create "refresh_tokens" table
CREATE TABLE "refresh_tokens" (
  "id" bigserial NOT NULL,
  "user_id" bigint NOT NULL,
  "token_hash" character varying(64) NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "revoked_at" timestamptz NULL,
  "replaced_by" bigint NULL,
  "created_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_refresh_tokens_expires_at" to table: "refresh_tokens"
CREATE INDEX "idx_refresh_tokens_expires_at" ON "refresh_tokens" ("expires_at");
-- Create index "idx_refresh_tokens_revoked_at" to table: "refresh_tokens"
CREATE INDEX "idx_refresh_tokens_revoked_at" ON "refresh_tokens" ("revoked_at");
-- Create index "idx_refresh_tokens_token_hash" to table: "refresh_tokens"
CREATE UNIQUE INDEX "idx_refresh_tokens_token_hash" ON "refresh_tokens" ("token_hash");
-- Create index "idx_refresh_tokens_user_id" to table: "refresh_tokens"
CREATE INDEX "idx_refresh_tokens_user_id" ON "refresh_tokens" ("user_id");
-- Create "users" table
CREATE TABLE "users" (
  "id" bigserial NOT NULL,
  "email" character varying(255) NOT NULL,
  "password_hash" text NOT NULL,
  "is_superuser" boolean NOT NULL DEFAULT false,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_users_email" to table: "users"
CREATE UNIQUE INDEX "idx_users_email" ON "users" ("email");
-- Modify "auth_group_permissions" table
ALTER TABLE "auth_group_permissions" ADD
CONSTRAINT "fk_auth_group_permissions_group" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, ADD
CONSTRAINT "fk_auth_group_permissions_permission" FOREIGN KEY ("permission_id") REFERENCES "permissions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Modify "auth_user_groups" table
ALTER TABLE "auth_user_groups" ADD
CONSTRAINT "fk_auth_user_groups_group" FOREIGN KEY ("group_id") REFERENCES "groups" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, ADD
CONSTRAINT "fk_auth_user_groups_user" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Modify "auth_user_permissions" table
ALTER TABLE "auth_user_permissions" ADD
CONSTRAINT "fk_auth_user_permissions_permission" FOREIGN KEY ("permission_id") REFERENCES "permissions" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION, ADD
CONSTRAINT "fk_auth_user_permissions_user" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION;
-- Modify "invitations" table
ALTER TABLE "invitations" ADD
CONSTRAINT "fk_invitations_invited_by" FOREIGN KEY ("invited_by_user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT, ADD
CONSTRAINT "fk_invitations_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "organization_members" table
ALTER TABLE "organization_members" ADD
CONSTRAINT "fk_organization_members_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, ADD
CONSTRAINT "fk_organization_members_user" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT;
-- Modify "project_revisions" table
ALTER TABLE "project_revisions" ADD
CONSTRAINT "fk_project_revisions_creator" FOREIGN KEY ("created_by") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT, ADD
CONSTRAINT "fk_project_revisions_project" FOREIGN KEY ("project_id") REFERENCES "projects" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Modify "projects" table
ALTER TABLE "projects" ADD
CONSTRAINT "fk_projects_creator" FOREIGN KEY ("created_by") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT, ADD
CONSTRAINT "fk_projects_head" FOREIGN KEY ("head_revision_id") REFERENCES "project_revisions" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD
CONSTRAINT "fk_projects_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Enforce project_revisions append-only at the database level (#101). GORM's
-- BeforeUpdate hook guards only the model API; a raw UPDATE, Exec, or
-- Session{SkipHooks:true} bypasses it. This trigger refuses every UPDATE, so
-- "the stored bytes are the bytes that were accepted" holds against all write
-- paths. INSERT and DELETE stay allowed — the table is append-only, and a
-- rollback may prune revisions.
--
-- Out-of-band on purpose: GORM cannot express a trigger, so the desired schema
-- `gombit db makemigrations` computes will not contain it and a later run may
-- propose DROPping it. Keep the object (re-add the hunk if that happens).
CREATE FUNCTION "forge_project_revisions_no_update"() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'project_revisions is append-only: UPDATE is not permitted (row id=%)', OLD.id
    USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "forge_project_revisions_no_update"
  BEFORE UPDATE ON "project_revisions"
  FOR EACH ROW EXECUTE FUNCTION "forge_project_revisions_no_update"();
