BEGIN;
SET LOCAL search_path = ipo_pos, pg_catalog;

ALTER TABLE ipos
    DROP CONSTRAINT ipos_catalogue_source_check,
    ADD CONSTRAINT ipos_catalogue_source_check CHECK (catalogue_source IN ('nse','filing')),
    ADD CONSTRAINT ipos_filing_schedule_check CHECK (catalogue_source <> 'filing' OR (
        opens_on IS NULL AND closes_on IS NULL AND listing_on IS NULL
        AND price_low IS NULL AND price_high IS NULL AND lot_size IS NULL
        AND (timeline#>>'{opening,status}') IS NOT DISTINCT FROM 'expected'
        AND (timeline#>>'{closing,status}') IS NOT DISTINCT FROM 'expected'
        AND (timeline#>>'{opening,precision}') IS NOT DISTINCT FROM 'date'
        AND (timeline#>>'{closing,precision}') IS NOT DISTINCT FROM 'date'
        AND COALESCE(pg_input_is_valid(timeline#>>'{opening,value}','date'),false)
        AND COALESCE(pg_input_is_valid(timeline#>>'{closing,value}','date'),false)
        AND (timeline#>>'{opening,value}') ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
        AND (timeline#>>'{closing,value}') ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
        AND (timeline#>>'{opening,value}') <= (timeline#>>'{closing,value}')
    ));

COMMIT;
