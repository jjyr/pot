-- +migrate Up
-- Price snapshots remain immutable. New snapshots retain models.dev provenance,
-- context tiers and unsupported-dimension evidence; manual price settings retire.
DROP TABLE model_prices;
