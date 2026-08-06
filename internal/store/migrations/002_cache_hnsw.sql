-- +migrate Up
-- HNSW for semantic cache; iterative_scan enabled per-query in app.
CREATE INDEX IF NOT EXISTS idx_cache_entries_embedding_hnsw
    ON cache_entries
    USING hnsw (embedding vector_cosine_ops);
