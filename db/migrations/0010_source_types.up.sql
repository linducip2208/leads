-- widen lead_sources type set for the source registry
ALTER TABLE lead_sources DROP CONSTRAINT IF EXISTS lead_sources_type_check;
ALTER TABLE lead_sources ADD CONSTRAINT lead_sources_type_check
    CHECK (type IN ('google_places','crawler','directory','csv','xlsx','manual','api','website_search','public_directory','custom_api','mock'));
