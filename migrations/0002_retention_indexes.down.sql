DROP INDEX IF EXISTS idx_outbox_events_published;
DROP INDEX IF EXISTS idx_subsystem_commands_pending_order;
DROP INDEX IF EXISTS idx_subsystem_commands_finished;
DROP INDEX IF EXISTS idx_container_moves_moved_at;
DROP INDEX IF EXISTS idx_container_tracks_active_order;
DROP INDEX IF EXISTS idx_transport_requests_rejected;
DROP INDEX IF EXISTS idx_transport_requests_order;
DROP INDEX IF EXISTS idx_transport_orders_open_container;
DROP INDEX IF EXISTS idx_transport_orders_ended;
DROP INDEX IF EXISTS idx_transport_legs_order; -- carry (retention review)
-- carry (retention review): the order id columns go back to the default collation, after their indexes.
ALTER TABLE transport_requests ALTER COLUMN transport_order_id TYPE TEXT COLLATE "default";
ALTER TABLE container_tracks ALTER COLUMN active_order_id TYPE TEXT COLLATE "default";
ALTER TABLE subsystem_commands ALTER COLUMN transport_order_id TYPE TEXT COLLATE "default";
ALTER TABLE transport_legs ALTER COLUMN transport_order_id TYPE TEXT COLLATE "default";
