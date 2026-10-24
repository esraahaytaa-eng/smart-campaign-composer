-- +goose Up
-- D68: an optional A/B test. Content gets a version (existing content is A),
-- and each message the version its recipient was assigned when the audience
-- was built. NULL on messages of campaigns without an A/B test.
ALTER TABLE campaigns ADD COLUMN ab_test boolean NOT NULL DEFAULT false;
ALTER TABLE campaign_content ADD COLUMN variant text NOT NULL DEFAULT 'a' CHECK (variant IN ('a', 'b'));
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    ADD PRIMARY KEY (campaign_id, channel, language, variant);
ALTER TABLE messages ADD COLUMN variant text CHECK (variant IN ('a', 'b'));

-- +goose Down
DELETE FROM campaign_content WHERE variant = 'b';
ALTER TABLE messages DROP COLUMN variant;
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    DROP COLUMN variant,
    ADD PRIMARY KEY (campaign_id, channel, language);
ALTER TABLE campaigns DROP COLUMN ab_test;
