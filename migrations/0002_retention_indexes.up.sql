-- The retention purges' lookups (ADR 0008): each purge finds its terminal rows by an index, and each "a live row still
-- references it" guard is an index probe, never a scan per candidate row.
-- carry (retention review): the FK cascade of an order's delete looks its legs up by transport_order_id, once per
-- deleted order; without this index every lookup was a sequential scan of transport_legs (13.3 s a batch at 300k).
CREATE INDEX idx_transport_legs_order ON transport_legs (transport_order_id);
CREATE INDEX idx_transport_orders_ended ON transport_orders (updated_at) WHERE status IN ('COMPLETED', 'FAILED');
CREATE INDEX idx_transport_orders_open_container ON transport_orders (container_id) WHERE status = 'IN_PROGRESS';
CREATE INDEX idx_transport_requests_order ON transport_requests (transport_order_id);
CREATE INDEX idx_transport_requests_rejected ON transport_requests (recorded_at) WHERE outcome = 'REJECTED';
CREATE INDEX idx_container_tracks_active_order ON container_tracks (active_order_id) WHERE active_order_id <> '';
-- carry (retention review): (moved_at, id) is the moves purge's keyset order.
CREATE INDEX idx_container_moves_moved_at ON container_moves (moved_at, id);
CREATE INDEX idx_subsystem_commands_finished ON subsystem_commands (created_at) WHERE status IN ('DONE', 'FAILED');
CREATE INDEX idx_subsystem_commands_pending_order ON subsystem_commands (transport_order_id) WHERE status = 'PENDING';
CREATE INDEX idx_outbox_events_published ON outbox_events (published_at) WHERE published_at IS NOT NULL;
