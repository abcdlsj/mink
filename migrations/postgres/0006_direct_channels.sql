CREATE TABLE direct_channels (
    channel_id UUID PRIMARY KEY,
    space_id UUID NOT NULL,
    member_low_id UUID NOT NULL,
    member_high_id UUID NOT NULL,
    UNIQUE (space_id, member_low_id, member_high_id),
    FOREIGN KEY (channel_id, space_id) REFERENCES channels(id, space_id),
    FOREIGN KEY (member_low_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (member_high_id, space_id) REFERENCES members(id, space_id),
    FOREIGN KEY (channel_id, member_low_id)
        REFERENCES channel_members(channel_id, member_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (channel_id, member_high_id)
        REFERENCES channel_members(channel_id, member_id)
        DEFERRABLE INITIALLY DEFERRED,
    CHECK (member_low_id < member_high_id)
);

CREATE FUNCTION enforce_direct_channel_member() RETURNS trigger AS $$
DECLARE
    direct_pair direct_channels%ROWTYPE;
BEGIN
    SELECT * INTO direct_pair FROM direct_channels WHERE channel_id = NEW.channel_id;
    IF FOUND AND NEW.member_id <> direct_pair.member_low_id
             AND NEW.member_id <> direct_pair.member_high_id THEN
        RAISE EXCEPTION 'direct Channel only accepts its two participants'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER channel_members_enforce_direct_pair
BEFORE INSERT OR UPDATE ON channel_members
FOR EACH ROW EXECUTE FUNCTION enforce_direct_channel_member();

CREATE FUNCTION validate_direct_channel_pair() RETURNS trigger AS $$
DECLARE
    channel_kind TEXT;
    participant_count BIGINT;
BEGIN
    SELECT kind INTO channel_kind FROM channels WHERE id = NEW.channel_id;
    IF channel_kind <> 'direct' THEN
        RAISE EXCEPTION 'direct_channels row requires a direct Channel'
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*) INTO participant_count
    FROM channel_members
    WHERE channel_id = NEW.channel_id
      AND member_id IN (NEW.member_low_id, NEW.member_high_id);
    IF participant_count <> 2 OR
       (SELECT count(*) FROM channel_members WHERE channel_id = NEW.channel_id) <> 2 THEN
        RAISE EXCEPTION 'direct Channel must contain exactly its two participants'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER direct_channels_validate_pair
BEFORE INSERT OR UPDATE ON direct_channels
FOR EACH ROW EXECUTE FUNCTION validate_direct_channel_pair();
