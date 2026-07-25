-- +goose Up
-- Modify "document" table
ALTER TABLE "silver"."document" ADD COLUMN "mapedges_at" timestamptz NULL;
