CREATE UNIQUE INDEX inbox_items_one_pending_thread_ambient_idx
    ON inbox_items (member_id, channel_id, thread_id)
    WHERE kind = 'thread_activity' AND thread_id IS NOT NULL AND status = 'pending';
