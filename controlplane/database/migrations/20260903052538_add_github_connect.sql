-- Create "connections" table
CREATE TABLE "connections" (
  "id" bigserial NOT NULL,
  "user_id" bigint NOT NULL,
  "access_token" text NOT NULL,
  "login" character varying(120) NULL,
  "created_at" timestamptz NULL,
  "updated_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_connections_user_id" to table: "connections"
CREATE UNIQUE INDEX "idx_connections_user_id" ON "connections" ("user_id");
-- Create "o_auth_states" table
CREATE TABLE "o_auth_states" (
  "id" bigserial NOT NULL,
  "state" character varying(64) NOT NULL,
  "user_id" bigint NOT NULL,
  "expires_at" timestamptz NOT NULL,
  "created_at" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_o_auth_states_expires_at" to table: "o_auth_states"
CREATE INDEX "idx_o_auth_states_expires_at" ON "o_auth_states" ("expires_at");
-- Create index "idx_o_auth_states_state" to table: "o_auth_states"
CREATE UNIQUE INDEX "idx_o_auth_states_state" ON "o_auth_states" ("state");
-- Create index "idx_o_auth_states_user_id" to table: "o_auth_states"
CREATE INDEX "idx_o_auth_states_user_id" ON "o_auth_states" ("user_id");
