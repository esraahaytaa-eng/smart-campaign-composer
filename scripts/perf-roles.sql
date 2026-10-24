-- make perf only (docker-compose.perf.yml): the api's own login role, a member
-- of scc with the same rights, so pg_stat_statements separates its database
-- time from the worker's. Runs once, when the throwaway database is created.
CREATE EXTENSION pg_stat_statements;
CREATE ROLE scc_api LOGIN PASSWORD 'scc-api-perf' IN ROLE scc;
