CREATE UNIQUE INDEX inbox_items_one_pending_channel_ambient_idx
    ON inbox_items (member_id, channel_id)
    WHERE kind = 'channel_activity' AND thread_id IS NULL AND status = 'pending';
