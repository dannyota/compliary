-- +goose Up
-- Create index "idx_silver_mapping_to_control" to table: "control_mapping"
CREATE INDEX "idx_silver_mapping_to_control" ON "silver"."control_mapping" ("to_control_id") WHERE (to_control_id IS NOT NULL);
