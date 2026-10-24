-- +goose Up
-- D64, D66: content per language, each language reviewed on its own, and a
-- default language as the fallback. The language checks follow
-- content.Languages plus 'und' (content written before languages were recorded).
CREATE TABLE campaign_languages (
    campaign_id bigint NOT NULL REFERENCES campaigns (id),
    language text NOT NULL CHECK (language IN ('tr', 'en', 'und')),
    approved_at timestamptz,
    approved_by bigint REFERENCES marketers (id),
    outdated boolean NOT NULL DEFAULT false,  -- the brief changed after the text was written
    PRIMARY KEY (campaign_id, language),
    CHECK ((approved_at IS NULL) = (approved_by IS NULL))
);
ALTER TABLE campaigns ADD COLUMN default_language text CHECK (default_language IN ('tr', 'en', 'und'));

-- Existing content becomes one approved version labelled 'und', sent to everyone as before.
INSERT INTO campaign_languages (campaign_id, language, approved_at, approved_by)
SELECT c.id, 'und', coalesce(c.approved_at, now()), coalesce(c.approved_by, c.created_by)
FROM campaigns c WHERE EXISTS (SELECT 1 FROM campaign_content cc WHERE cc.campaign_id = c.id);
UPDATE campaigns c SET default_language = 'und'
WHERE EXISTS (SELECT 1 FROM campaign_content cc WHERE cc.campaign_id = c.id);

ALTER TABLE campaign_content ADD COLUMN language text NOT NULL DEFAULT 'und';
ALTER TABLE campaign_content ALTER COLUMN language DROP DEFAULT;
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    ADD PRIMARY KEY (campaign_id, channel, language),
    ADD FOREIGN KEY (campaign_id, language) REFERENCES campaign_languages ON DELETE CASCADE;

-- +goose Down
-- Each campaign keeps its default language's text, the one it falls back to.
DELETE FROM campaign_content cc USING campaigns c
WHERE c.id = cc.campaign_id AND cc.language IS DISTINCT FROM c.default_language;
ALTER TABLE campaign_content DROP CONSTRAINT campaign_content_pkey,
    DROP COLUMN language,
    ADD PRIMARY KEY (campaign_id, channel);
ALTER TABLE campaigns DROP COLUMN default_language;
DROP TABLE campaign_languages;
